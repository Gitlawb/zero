package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/redaction"
)

func (m model) mcpAddWizardOverlay(width int) string {
	if m.mcpAddWizard == nil {
		return ""
	}
	return m.mcpAddWizard.render(width)
}

func (wizard *mcpAddWizardState) render(width int) string {
	if wizard == nil {
		return ""
	}
	overlayWidth := mcpAddWizardOverlayWidth(width)
	innerWidth := maxInt(20, overlayWidth-4)
	lines := []string{
		zeroTheme.faint.Render(mcpAddWizardStepLine(wizard.step)),
		zeroTheme.line.Render(strings.Repeat("-", innerWidth)),
	}
	if wizard.err != "" {
		lines = append(lines, zeroTheme.red.Render("error: "+wizard.err), "")
	}
	switch wizard.step {
	case mcpAddWizardStepName:
		lines = append(lines, wizard.renderNameStep(innerWidth)...)
	case mcpAddWizardStepType:
		lines = append(lines, wizard.renderTypeStep(innerWidth)...)
	case mcpAddWizardStepEndpoint:
		lines = append(lines, wizard.renderEndpointStep(innerWidth)...)
	case mcpAddWizardStepHeader:
		lines = append(lines, wizard.renderHeaderStep(innerWidth)...)
	case mcpAddWizardStepConfirm:
		lines = append(lines, wizard.renderConfirmStep(innerWidth)...)
	case mcpAddWizardStepResult:
		lines = append(lines, wizard.renderResultStep(innerWidth)...)
	}
	lines = append(lines,
		zeroTheme.line.Render(strings.Repeat("-", innerWidth)),
		zeroTheme.faint.Render(wizard.footer()),
	)
	block := styledBlockFillTitle(overlayWidth, "Add MCP Server", lines, zeroTheme.lineStrong, lipgloss.NewStyle())
	return centerRenderedBlock(block, width)
}

func (wizard *mcpAddWizardState) renderNameStep(width int) []string {
	value := displayValue(strings.TrimSpace(wizard.serverName), "type a stable name")
	return []string{
		zeroTheme.accent.Render("Server Name"),
		fitStyledLine(zeroTheme.ink.Render("> "+value), width),
		zeroTheme.faint.Render("Default: " + mcpAddWizardTypes[clampInt(wizard.selectedType, 0, len(mcpAddWizardTypes)-1)].Label),
		zeroTheme.faint.Render("Use lowercase letters, numbers, dashes, or underscores."),
	}
}

func (wizard *mcpAddWizardState) renderTypeStep(width int) []string {
	lines := []string{zeroTheme.accent.Render("Server Type")}
	for index, item := range mcpAddWizardTypes {
		marker := "  "
		surface := transparentSurface
		if index == wizard.selectedType {
			marker = "> "
			surface = zeroTheme.onSel
		}
		line := marker + item.Label
		if item.Meta != "" {
			line += "  " + item.Meta
		}
		lines = append(lines, fillPaletteLine(surface(zeroTheme.ink).Render(line), width, surface))
	}
	return lines
}

func (wizard *mcpAddWizardState) renderEndpointStep(width int) []string {
	title := "Endpoint URL"
	placeholder := "https://example.com/mcp"
	if !wizard.isRemote() {
		title = "Command"
		placeholder = "npx -y @modelcontextprotocol/server-filesystem ."
	}
	value := displayValue(strings.TrimSpace(wizard.endpoint), placeholder)
	return []string{
		zeroTheme.accent.Render(title),
		fitStyledLine(zeroTheme.ink.Render("> "+value), width),
	}
}

func (wizard *mcpAddWizardState) renderHeaderStep(width int) []string {
	value := displayValue(strings.TrimSpace(wizard.headerInput), "press Enter to skip")
	return []string{
		zeroTheme.accent.Render("Add header"),
		fitStyledLine(zeroTheme.ink.Render("> "+redaction.RedactString(value, redaction.Options{})), width),
		zeroTheme.faint.Render(`Paste "Key: Value" or "Key=Value".`),
	}
}

func (wizard *mcpAddWizardState) renderConfirmStep(width int) []string {
	lines := []string{
		zeroTheme.accent.Render("Review setup"),
		"server: " + zeroTheme.ink.Render(wizard.serverName),
		"type: " + zeroTheme.ink.Render(strings.ToUpper(wizard.serverType)),
	}
	if wizard.isRemote() {
		lines = append(lines, "url: "+zeroTheme.ink.Render(wizard.endpoint))
		if wizard.headerKey != "" {
			lines = append(lines, "header: "+zeroTheme.ink.Render(wizard.headerKey+"=[REDACTED]"))
		}
	} else {
		lines = append(lines, "command: "+zeroTheme.ink.Render(wizard.endpoint))
	}
	lines = append(lines, "", zeroTheme.faint.Render("Enter saves and tests the server."))
	for index, line := range lines {
		lines[index] = fitStyledLine(line, width)
	}
	return lines
}

func (wizard *mcpAddWizardState) renderResultStep(width int) []string {
	result := wizard.result
	title := result.Title
	if title == "" {
		title = "MCP setup issue"
	}
	state := displayValue(result.State, "not saved")
	transport := "HTTP remote"
	if !wizard.isRemote() {
		transport = "Local stdio"
	} else if wizard.serverType == "sse" {
		transport = "SSE remote"
	}
	lines := []string{
		zeroTheme.accent.Render(title),
		zeroTheme.ink.Bold(true).Render(displayValue(wizard.serverName, "unnamed")) + "  " + zeroTheme.faint.Render(state),
		zeroTheme.faint.Render(transport),
	}
	if result.Message != "" {
		lines = append(lines, fitStyledLine(zeroTheme.red.Render(result.Message), width))
	}
	if !result.Saved {
		lines = append(lines, zeroTheme.faint.Render("No config was saved yet."))
	} else {
		lines = append(lines, fmt.Sprintf("Tools: %d discovered", maxInt(0, result.ToolCount)))
	}
	lines = append(lines, "")
	if result.Connected {
		lines = append(lines, "> Use server", "  Manage tools", "  Edit config", "  Disable server")
	} else if result.Saved {
		lines = append(lines, "> Retry connection", "  Edit config", "  Disable server", "  Remove server")
	} else {
		lines = append(lines, "> Edit URL", "  Save disabled", "  Discard")
	}
	for index, line := range lines {
		lines[index] = fitStyledLine(line, width)
	}
	return lines
}

func (wizard *mcpAddWizardState) footer() string {
	switch wizard.step {
	case mcpAddWizardStepType:
		return "up/down select   Enter continue   Esc close"
	case mcpAddWizardStepResult:
		return "Enter select   r retry   s save disabled   d discard"
	default:
		return "Enter continue   left back   Esc close"
	}
}

func mcpAddWizardOverlayWidth(width int) int {
	if width <= 0 {
		return mcpAddWizardMaxWidth
	}
	target := minInt(width, mcpAddWizardMaxWidth)
	if target < mcpAddWizardMinWidth {
		return width
	}
	return target
}

func mcpAddWizardStepLine(step mcpAddWizardStep) string {
	steps := []struct {
		step  mcpAddWizardStep
		label string
	}{
		{mcpAddWizardStepName, "1 name"},
		{mcpAddWizardStepType, "2 type"},
		{mcpAddWizardStepEndpoint, "3 endpoint"},
		{mcpAddWizardStepHeader, "4 auth"},
		{mcpAddWizardStepConfirm, "5 confirm"},
	}
	parts := make([]string, 0, len(steps))
	for _, item := range steps {
		if item.step == step {
			parts = append(parts, "["+item.label+"]")
		} else {
			parts = append(parts, item.label)
		}
	}
	return strings.Join(parts, "  ")
}
