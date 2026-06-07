package sandbox

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	networkCommandPattern = regexp.MustCompile(`(?i)\b(curl|wget|scp|ssh|rsync|nc|netcat|python3?\s+-m\s+http\.server|npm\s+(install|add|publish|login)|pnpm\s+(install|add|publish)|yarn\s+(add|publish)|bun\s+(add|install|publish)|pip3?\s+install|go\s+get|git\s+clone|gh\s+(release\s+download|repo\s+clone|api))\b`)
	// destructiveCommandPattern matches the highest-risk shell forms:
	//   - rm -rf (with combined/reordered r/f flags) targeting /, $HOME (bare,
	//     quoted, or ${HOME} braced), ~, or *, with an optional `--` before the
	//     target. Each target alternative tolerates optional surrounding quotes
	//     so `rm -rf "/"` / `rm -rf '/'` cannot slip past the gate.
	//   - chmod with combined/reordered flags and an octal-or-777 mode applied
	//     RECURSIVELY (a -R/-r flag) or to root / a sensitive SYSTEM tree
	//     (/, /etc, /usr, /bin, /var, … — e.g. chmod -Rf 777 /, chmod -R 0777 /,
	//     chmod 777 -R /etc, chmod 777 /etc). A single-file chmod 777 — including
	//     an absolute non-system path like `chmod 777 /tmp/build.sh` or a relative
	//     `chmod 777 script.sh` — is intentionally NOT flagged; the intent is
	//     recursive/directory-tree or system-tree chmod.
	//   - mkfs, dd if=, chown -R.
	destructiveCommandPattern = regexp.MustCompile(`(?i)(\brm\s+(-[A-Za-z]*r[A-Za-z]*f|-rf|-fr)\s+(--\s+)?["']?(\$\{?HOME\}?|/|~|\*)["']?|\bmkfs\b|\bdd\s+if=|\bchmod\s+(-[A-Za-z]*[rR][A-Za-z]*\s+)+0?777\b|\bchmod\s+(-\S+\s+)*0?777\s+-[A-Za-z]*[rR][A-Za-z]*\b|\bchmod\s+(-\S+\s+)*0?777\s+["']?/(\s|$|["']|(etc|usr|bin|sbin|lib|lib64|var|boot|opt|root|sys|proc|dev)\b)|\bchown\s+-R\b)`)
	// pipedInstallerPattern matches the fetch-and-execute idiom: a remote fetch
	// (curl/wget/fetch/aria2c) piped into a POSIX shell, with or without a space
	// and across sh/bash/zsh/ksh/dash (so `curl x|sh`, `wget url | bash`, `| zsh`).
	// A purely local pipe into a shell (e.g. `printf … | sh`, `cat ./s | bash`)
	// is NOT a piped installer and must not be flagged.
	pipedInstallerPattern = regexp.MustCompile(`(?i)\b(curl|wget|fetch|aria2c)\b[^|]*\|\s*(ba|z|k|da)?sh\b`)
	// destructiveExtraPatterns hold high-severity patterns that the legacy
	// destructiveCommandPattern does not already cover. Folded in from the
	// blueprint safe_bash.go without duplicating existing matches.
	destructiveExtraPatterns = []*regexp.Regexp{
		// Fork bomb (and minor spacing variants).
		regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
		// Writing to a raw block device (dd of=, redirect to /dev/sdX, etc.).
		regexp.MustCompile(`(?i)>\s*/dev/(sd[a-z]+\d*|nvme\d+n\d+(p\d+)?|hd[a-z]+\d*|xvd[a-z]+\d*|mmcblk\d+)`),
		regexp.MustCompile(`(?i)\bof=/dev/(sd[a-z]+\d*|nvme\d+n\d+(p\d+)?|hd[a-z]+\d*|xvd[a-z]+\d*|mmcblk\d+)`),
		// rm targeting a dangerous root (/, /*, ~, $HOME, *) with ANY mix of
		// short/long flags (incl. --no-preserve-root) in any order, an optional
		// `--` separator, and optional surrounding quotes — so e.g.
		// `rm --no-preserve-root -rf -- "/"` and `rm --no-preserve-root -rf "/"`
		// cannot slip past the gate.
		regexp.MustCompile(`(?i)\brm\s+(-{1,2}\S+\s+)*(--\s+)?["']?(/\*?|~|\$\{?HOME\}?|\*)["']?(\s|$)`),
		// mkfs.<fstype> form (e.g. mkfs.ext4) not caught by the bare \bmkfs\b above when followed by a dot.
		regexp.MustCompile(`(?i)\bmkfs\.[a-z0-9]+\b`),
	}
)

func matchesDestructive(command string) bool {
	if destructiveCommandPattern.MatchString(command) {
		return true
	}
	for _, pattern := range destructiveExtraPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

func Classify(request Request) Risk {
	categories := map[string]bool{}
	level := RiskLow
	add := func(category string, risk RiskLevel) {
		categories[category] = true
		if riskRank(risk) > riskRank(level) {
			level = risk
		}
	}

	switch NormalizeSideEffect(request.SideEffect) {
	case SideEffectRead:
		add("read", RiskLow)
	case SideEffectWrite:
		add("write", RiskMedium)
	case SideEffectShell:
		add("shell", RiskHigh)
	case SideEffectNetwork:
		add("network", RiskHigh)
	case SideEffectOutOfWorkspace:
		add("out_of_workspace", RiskCritical)
	}

	// The bash tool accepts the command under any of these aliases; resolve the
	// first non-empty so destructive/network/piped-installer classification
	// cannot be bypassed by choosing a different alias key.
	command := firstArgString(request.Args, "command", "cmd", "script", "shell")
	if command != "" {
		if networkCommandPattern.MatchString(command) {
			add("network", RiskCritical)
		}
		if matchesDestructive(command) {
			add("destructive", RiskCritical)
		}
		if pipedInstallerPattern.MatchString(command) {
			add("piped_installer", RiskCritical)
		}
	}

	for _, path := range requestPaths(request) {
		if filepath.IsAbs(path) {
			add("absolute_path", RiskMedium)
		}
		if path == ".." || strings.HasPrefix(filepath.ToSlash(filepath.Clean(path)), "../") {
			add("path_escape", RiskCritical)
		}
		if request.WorkspaceRoot != "" {
			if violation := validateWorkspacePath(request.WorkspaceRoot, path); violation != nil {
				switch violation.Code {
				case ViolationSymlinkTraversal:
					add("symlink_traversal", RiskCritical)
				default:
					add("out_of_workspace", RiskCritical)
				}
			}
		}
	}

	names := make([]string, 0, len(categories))
	for category := range categories {
		names = append(names, category)
	}
	sort.Strings(names)
	return Risk{
		Level:      level,
		Categories: names,
		Reason:     riskReason(level, names),
	}
}

func HasRiskCategory(risk Risk, category string) bool {
	for _, candidate := range risk.Categories {
		if candidate == category {
			return true
		}
	}
	return false
}

func riskRank(level RiskLevel) int {
	switch level {
	case RiskLow:
		return 0
	case RiskMedium:
		return 1
	case RiskHigh:
		return 2
	case RiskCritical:
		return 3
	default:
		return 0
	}
}

func riskReason(level RiskLevel, categories []string) string {
	if len(categories) == 0 {
		return string(level)
	}
	return fmt.Sprintf("%s risk: %s", level, strings.Join(categories, ", "))
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

// firstArgString returns the first non-empty argument value among keys.
func firstArgString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := argString(args, key); value != "" {
			return value
		}
	}
	return ""
}

func requestPaths(request Request) []string {
	paths := []string{}
	for _, key := range []string{"path", "cwd", "file", "dir"} {
		if value := argString(request.Args, key); value != "" {
			paths = append(paths, value)
		}
	}
	return paths
}
