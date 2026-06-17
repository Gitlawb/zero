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
	// catastrophicCommandPattern mirrors destructiveCommandPattern but restricts
	// the rm target to the IRRECOVERABLE / workspace-escaping forms — a system
	// root (/), $HOME (bare/braced), ~, or a path-traversal escape (..) — while
	// keeping the always-irrecoverable non-rm forms (mkfs, dd if=, recursive or
	// system chmod 777, chown -R). A bare-glob delete (rm -rf *) is deliberately
	// ABSENT: it is workspace-local and the engine downgrades it to a prompt
	// rather than hard-denying it. Everything this matches stays a hard block
	// even in unsafe mode.
	catastrophicCommandPattern = regexp.MustCompile(`(?i)(\brm\s+(-[A-Za-z]*r[A-Za-z]*f|-rf|-fr)\s+(--\s+)?["']?(\$\{?HOME\}?|/|~|\.\.)["']?|\bmkfs\b|\bdd\s+if=|\bchmod\s+(-[A-Za-z]*[rR][A-Za-z]*\s+)+0?777\b|\bchmod\s+(-\S+\s+)*0?777\s+-[A-Za-z]*[rR][A-Za-z]*\b|\bchmod\s+(-\S+\s+)*0?777\s+["']?/(\s|$|["']|(etc|usr|bin|sbin|lib|lib64|var|boot|opt|root|sys|proc|dev)\b)|\bchown\s+-R\b)`)
	// catastrophicExtraPatterns mirror destructiveExtraPatterns: the device/fork-
	// bomb forms are unconditionally catastrophic; the rm form drops the bare-glob
	// target (kept scoped) and adds a path-traversal target (..) so a workspace
	// escape stays denied. mkfs.<fstype> is likewise catastrophic.
	catastrophicExtraPatterns = []*regexp.Regexp{
		regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
		regexp.MustCompile(`(?i)>\s*/dev/(sd[a-z]+\d*|nvme\d+n\d+(p\d+)?|hd[a-z]+\d*|xvd[a-z]+\d*|mmcblk\d+)`),
		regexp.MustCompile(`(?i)\bof=/dev/(sd[a-z]+\d*|nvme\d+n\d+(p\d+)?|hd[a-z]+\d*|xvd[a-z]+\d*|mmcblk\d+)`),
		regexp.MustCompile(`(?i)\brm\s+(-{1,2}\S+\s+)*(--\s+)?["']?(/\*?|~|\$\{?HOME\}?|\.\.)["']?(\s|$|/)`),
		regexp.MustCompile(`(?i)\bmkfs\.[a-z0-9]+\b`),
	}
	// pathTraversalPattern matches a `..` path component (../, /.., or a bare ..).
	// Combined with a destructive classification it flags a command whose target
	// escapes the workspace — fail-closed: a destructive command containing any
	// `..` component is treated as catastrophic even if it would resolve inside
	// the workspace, because the regex matchers cannot prove it does.
	pathTraversalPattern = regexp.MustCompile(`(^|[\s"'=:(/])\.\.(/|$|[\s"'])`)
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

// matchesCatastrophic reports the irrecoverable / workspace-escaping subset of
// matchesDestructive: the forms that stay a hard deny even in unsafe mode. It is
// strictly narrower than matchesDestructive — a workspace-local delete such as
// `rm -rf *` or `rm -rf <subdir>` is destructive but NOT catastrophic, so the
// engine can downgrade it to a prompt instead of hard-blocking it.
func matchesCatastrophic(command string) bool {
	if catastrophicCommandPattern.MatchString(command) {
		return true
	}
	for _, pattern := range catastrophicExtraPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// commandHasPathTraversal reports whether the command contains a `..` path
// component. Used only in combination with a destructive classification to keep
// a workspace-escaping delete (e.g. `rm -rf ../../`) catastrophic even when the
// regex matchers — which key off system-root/$HOME/~ targets — do not match it.
func commandHasPathTraversal(command string) bool {
	return pathTraversalPattern.MatchString(command)
}

func Classify(request Request) Risk {
	return classifyWithScope(request, nil)
}

func classifyWithScope(request Request, scope *Scope) Risk {
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
	case SideEffectNone:
		// Control-only tool (e.g. escalate_model): no read/write/shell/network
		// effect, so it contributes no side-effect risk category and stays low.
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
		if matchesCatastrophic(command) {
			// destructive_catastrophic marks the irrecoverable, system-level or
			// workspace-escaping forms (rm -rf / or $HOME/~/.., mkfs, dd to a raw
			// device, fork bomb, chmod 777 on a system tree, chown -R). These stay a
			// hard block even in unsafe mode. A workspace-local destructive command
			// (rm -rf * or rm -rf <subdir>) is "destructive" but NOT catastrophic, so
			// the engine downgrades it to a prompt rather than hard-blocking it.
			add("destructive_catastrophic", RiskCritical)
		}
		if pipedInstallerPattern.MatchString(command) {
			add("piped_installer", RiskCritical)
		}
		// AST second opinion (analyzer.go): walks the parsed shell tree, so it
		// catches destructive/network programs the regexes miss — e.g. shred,
		// fdisk, parted, and commands hidden behind sudo/env wrappers or a
		// `sh -c <payload>` launcher — and flags an unparseable (obfuscated)
		// script as elevated risk. It only ADDS categories, so a benign,
		// parseable command is classified exactly as before.
		analysis := AnalyzeCommand(command)
		if analysis.Network {
			add("network", RiskCritical)
		}
		if analysis.Destructive {
			add("destructive", RiskCritical)
			// A destructive command whose target escapes the workspace via path
			// traversal (..) is catastrophic and must stay denied even in unsafe
			// mode. The regex matchers only catch system-root/$HOME/~ targets, so the
			// AST (which flags `rm -rf <anything>` regardless of flag order) plus a
			// simple `..` check closes the `rm -rf ../../` escape Vasanth flagged.
			if commandHasPathTraversal(command) {
				add("destructive_catastrophic", RiskCritical)
			}
		}
		if analysis.TooComplex {
			add("unparseable_command", RiskHigh)
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
			var violation *pathViolation
			if scope != nil {
				violation = scope.validate(path)
			} else {
				violation = validateWorkspacePath(request.WorkspaceRoot, path)
			}
			if violation != nil {
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
	// Keep this aligned with the path-arg alias lists the tools accept (see
	// aliasedStringArg in write_file/edit_file/read_file/grep/glob/list). The
	// sandbox gates by arg-key name, so any alias a tool resolves but the sandbox
	// does not inspect would let a model route a write/read around the
	// workspace+symlink boundary.
	for _, key := range []string{"path", "file", "file_path", "filepath", "filename", "cwd", "dir", "directory"} {
		if value := argString(request.Args, key); value != "" {
			paths = append(paths, value)
		}
	}
	return paths
}
