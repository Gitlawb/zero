package specialist

import (
	"fmt"
	"strings"
)

func FormatList(result LoadResult) string {
	lines := []string{"Zero Specialists:"}
	for _, warning := range result.Warnings {
		lines = append(lines, "  warning: "+warning)
	}
	if len(result.Specialists) == 0 {
		lines = append(lines, "  (none)")
		return strings.Join(lines, "\n")
	}
	for _, manifest := range result.Specialists {
		lines = append(lines, fmt.Sprintf("  %s [%s] - %s", manifest.Metadata.Name, manifest.Location, manifest.Metadata.Description))
		tools := formatTools(manifest)
		if tools != "" {
			lines = append(lines, "    tools: "+tools)
		}
		if len(manifest.Warnings) > 0 {
			lines = append(lines, "    warnings: "+strings.Join(manifest.Warnings, "; "))
		}
	}
	return strings.Join(lines, "\n")
}

func FormatShow(manifest Manifest) string {
	lines := []string{
		"Zero Specialist: " + manifest.Metadata.Name,
		"location: " + string(manifest.Location),
		"path: " + manifest.FilePath,
		"description: " + manifest.Metadata.Description,
	}
	if manifest.Metadata.Model != "" {
		lines = append(lines, "model: "+manifest.Metadata.Model)
	}
	if manifest.Metadata.Extends != "" {
		lines = append(lines, "extends: "+manifest.Metadata.Extends)
	}
	if manifest.Metadata.ReasoningEffort != "" {
		lines = append(lines, "reasoning effort: "+manifest.Metadata.ReasoningEffort)
	}
	tools := formatTools(manifest)
	if tools != "" {
		lines = append(lines, "tools: "+tools)
	}
	if len(manifest.Warnings) > 0 {
		lines = append(lines, "warnings: "+strings.Join(manifest.Warnings, "; "))
	}
	lines = append(lines, "", strings.TrimSpace(manifest.SystemPrompt))
	return strings.Join(lines, "\n")
}

func FormatPaths(paths Paths) string {
	lines := []string{
		"Zero Specialist Paths:",
		"  user: " + paths.UserDir,
	}
	if paths.ProjectDir != "" {
		lines = append(lines, "  project: "+paths.ProjectDir)
	}
	return strings.Join(lines, "\n")
}

func formatTools(manifest Manifest) string {
	if len(manifest.Metadata.Tools) == 0 && len(manifest.ResolvedTools) > 0 {
		return strings.Join(manifest.ResolvedTools, ", ") + " (default)"
	}
	if len(manifest.ResolvedTools) == 0 {
		return strings.Join(manifest.Metadata.Tools, ", ")
	}
	return strings.Join(manifest.ResolvedTools, ", ")
}
