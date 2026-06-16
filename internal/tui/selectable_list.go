package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

type selectableListItem struct {
	Label       string
	Description string
}

type selectableListOptions struct {
	Items      []selectableListItem
	Selected   int
	Width      int
	MaxVisible int
}

const selectableListAnchorRow = 3

func renderSelectableList(options selectableListOptions) string {
	if len(options.Items) == 0 {
		return ""
	}
	width := options.Width
	if width <= 0 {
		width = 80
	}
	maxVisible := options.MaxVisible
	if maxVisible <= 0 || maxVisible > len(options.Items) {
		maxVisible = len(options.Items)
	}
	selected := clampInt(options.Selected, 0, len(options.Items)-1)
	start := selectableListStart(len(options.Items), maxVisible, selected)
	visible := options.Items[start : start+maxVisible]

	labelWidth := 0
	for _, item := range visible {
		if w := lipgloss.Width(item.Label); w > labelWidth {
			labelWidth = w
		}
	}

	lines := make([]string, 0, maxVisible+1)
	for index, item := range visible {
		absoluteIndex := start + index
		surface := zeroTheme.onPanel
		marker := surface(zeroTheme.faintest).Render("  ")
		if absoluteIndex == selected {
			surface = zeroTheme.onSel
			marker = surface(zeroTheme.accent).Render("❯ ")
		}

		label := surface(zeroTheme.ink).Render(item.Label)
		pad := surface(zeroTheme.ink).Render(strings.Repeat(" ", maxInt(0, labelWidth-lipgloss.Width(item.Label))))
		line := marker + label + pad
		if strings.TrimSpace(item.Description) != "" {
			descWidth := width - lipgloss.Width(marker) - labelWidth - 2
			desc := truncateRunes(item.Description, maxInt(0, descWidth))
			if desc != "" {
				line += surface(zeroTheme.faint).Render("  " + desc)
			}
		}
		lines = append(lines, fitStyledLine(line, width))
	}

	if hidden := len(options.Items) - len(visible); hidden > 0 {
		lines = append(lines, fitStyledLine(zeroTheme.faint.Render(fmt.Sprintf("  %d more", hidden)), width))
	}
	return strings.Join(lines, "\n")
}

func selectableListStart(total, maxVisible, selected int) int {
	if total <= maxVisible {
		return 0
	}
	start := selected - selectableListAnchorRow
	return clampInt(start, 0, total-maxVisible)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
