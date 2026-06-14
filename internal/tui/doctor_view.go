package tui

import (
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/doctor"
	"github.com/Gitlawb/zero/internal/zerocommands"
)

func doctorCommandOutput(report doctor.Report, backend *zerocommands.BackendLifecycleSnapshot) commandOutput {
	sections := []commandSection{}
	if report.GeneratedAt != "" {
		sections = append(sections, commandSection{
			Title: "Summary",
			Fields: []commandField{
				{Key: "Generated", Value: report.GeneratedAt},
				{Key: "Checks", Value: fmt.Sprintf("%d", len(report.Checks))},
			},
		})
	}

	provider, platform, other := doctorCheckSections(report.Checks)
	sections = appendNonEmptyDoctorSection(sections, "Provider", provider)
	sections = appendNonEmptyDoctorSection(sections, "Platform", platform)
	sections = appendNonEmptyDoctorSection(sections, "Other", other)
	if backend != nil {
		sections = append(sections, doctorBackendSection(*backend))
	}

	return commandOutput{
		Title:    "Diagnostics",
		Status:   doctorCommandStatus(report),
		Sections: sections,
		Hints:    doctorHints(report.Checks, backend),
	}
}

func doctorCommandStatus(report doctor.Report) commandStatus {
	hasWarning := false
	for _, check := range report.Checks {
		switch check.Status {
		case doctor.StatusFail:
			return commandStatusBlocked
		case doctor.StatusWarn:
			hasWarning = true
		}
	}
	if hasWarning {
		return commandStatusWarning
	}
	return commandStatusOK
}

func doctorCheckSections(checks []doctor.Check) (provider []commandRow, platform []commandRow, other []commandRow) {
	for _, check := range checks {
		row := commandRow{Text: doctorCheckRow(check)}
		switch doctorCheckGroup(check.ID) {
		case "provider":
			provider = append(provider, row)
		case "platform":
			platform = append(platform, row)
		default:
			other = append(other, row)
		}
	}
	return provider, platform, other
}

func doctorCheckGroup(id string) string {
	switch id {
	case "provider.config", "provider.model", "provider.connectivity", "provider.auth", "provider.runtime":
		return "provider"
	case "sandbox.backend", "lsp.servers", "runtime.go", "config.files", "config.validation":
		return "platform"
	default:
		return ""
	}
}

func doctorCheckRow(check doctor.Check) string {
	parts := []string{fmt.Sprintf("[%s]", check.Status)}
	if check.ID != "" {
		parts = append(parts, check.ID)
	}
	if check.Message != "" {
		parts = append(parts, "-", check.Message)
	}
	return strings.Join(parts, " ")
}

func appendNonEmptyDoctorSection(sections []commandSection, title string, rows []commandRow) []commandSection {
	if len(rows) == 0 {
		return sections
	}
	return append(sections, commandSection{
		Title: title,
		Rows:  rows,
	})
}

func doctorBackendSection(backend zerocommands.BackendLifecycleSnapshot) commandSection {
	return commandSection{
		Title: "Backend",
		Fields: []commandField{
			{Key: "MCP servers", Value: fmt.Sprintf("%d", len(backend.MCPServers))},
			{Key: "Hooks", Value: fmt.Sprintf("%d", len(backend.Hooks))},
			{Key: "Plugins", Value: fmt.Sprintf("%d", len(backend.Plugins))},
		},
	}
}

func doctorHints(checks []doctor.Check, backend *zerocommands.BackendLifecycleSnapshot) []string {
	seen := map[string]bool{}
	hints := []string{}
	add := func(hint string) {
		hint = strings.TrimSpace(hint)
		if hint == "" || seen[hint] {
			return
		}
		seen[hint] = true
		hints = append(hints, hint)
	}

	for _, check := range checks {
		if check.Status == doctor.StatusPass {
			continue
		}
		message := strings.ToLower(check.Message)
		switch check.ID {
		case "provider.config", "provider.model":
			add("use /provider to configure the active provider and model")
		case "provider.connectivity":
			add("use /doctor --connectivity to retry provider connectivity checks")
		case "sandbox.backend":
			if strings.Contains(message, "windows") || strings.Contains(message, "policy-only") {
				add("run Zero inside WSL2 or a Linux container for native sandbox isolation on Windows")
			}
		case "lsp.servers":
			if strings.Contains(message, "missing") {
				add("install missing language servers so code intelligence is available on PATH")
			}
		}
	}

	if backend != nil {
		add("use /mcp to inspect MCP servers")
		add("use /hooks to inspect hooks")
		add("use /plugins to inspect plugins")
	}
	return hints
}
