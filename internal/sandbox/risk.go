package sandbox

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	networkCommandPattern     = regexp.MustCompile(`(?i)\b(curl|wget|scp|ssh|rsync|nc|netcat|python3?\s+-m\s+http\.server|npm\s+(install|add|publish|login)|pnpm\s+(install|add|publish)|yarn\s+(add|publish)|bun\s+(add|install|publish)|pip3?\s+install|go\s+get|git\s+clone|gh\s+(release\s+download|repo\s+clone|api))\b`)
	destructiveCommandPattern = regexp.MustCompile(`(?i)(\brm\s+(-[A-Za-z]*r[A-Za-z]*f|-rf|-fr)\s+(/|\$HOME|~|\*)|\bmkfs\b|\bdd\s+if=|\bchmod\s+-R\s+777\b|\bchown\s+-R\b)`)
)

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

	command := argString(request.Args, "command")
	if command != "" {
		if networkCommandPattern.MatchString(command) {
			add("network", RiskCritical)
		}
		if destructiveCommandPattern.MatchString(command) {
			add("destructive", RiskCritical)
		}
		lowerCommand := strings.ToLower(command)
		if strings.Contains(lowerCommand, "| sh") || strings.Contains(lowerCommand, "| bash") {
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

func requestPaths(request Request) []string {
	paths := []string{}
	for _, key := range []string{"path", "cwd", "file", "dir"} {
		if value := argString(request.Args, key); value != "" {
			paths = append(paths, value)
		}
	}
	return paths
}
