package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestSelectableListAnchorsSelectionAroundThirdVisibleRow(t *testing.T) {
	items := make([]selectableListItem, 10)
	for i := range items {
		items[i] = selectableListItem{Label: fmt.Sprintf("/cmd%d", i), Description: "command"}
	}

	rendered := renderSelectableList(selectableListOptions{
		Items:      items,
		Selected:   5,
		Width:      40,
		MaxVisible: 5,
	})
	lines := strings.Split(plainRender(t, rendered), "\n")

	if len(lines) != 6 {
		t.Fatalf("rendered %d lines, want 5 rows plus count line: %q", len(lines), lines)
	}
	for _, hidden := range []string{"/cmd0", "/cmd1", "/cmd2", "/cmd8", "/cmd9"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("visible window should hide %s, got %q", hidden, plainRender(t, rendered))
		}
	}
	for _, visible := range []string{"/cmd3", "/cmd4", "/cmd5", "/cmd6", "/cmd7"} {
		if !strings.Contains(rendered, visible) {
			t.Fatalf("visible window should include %s, got %q", visible, plainRender(t, rendered))
		}
	}
	if !strings.Contains(lines[2], "❯ /cmd5") {
		t.Fatalf("selected item should be anchored on third visible row, got %q", lines[2])
	}
	if !strings.Contains(lines[5], "5 more") {
		t.Fatalf("count line should summarize hidden rows, got %q", lines[5])
	}
}

func TestSelectableListAlignsAndTruncatesDescriptions(t *testing.T) {
	rendered := renderSelectableList(selectableListOptions{
		Items: []selectableListItem{
			{Label: "/m", Description: "Short"},
			{Label: "/model", Description: "Switch model and provider for this session"},
		},
		Selected:   0,
		Width:      32,
		MaxVisible: 8,
	})
	lines := strings.Split(plainRender(t, rendered), "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered lines = %d, want 2: %q", len(lines), lines)
	}

	shortAt := strings.Index(lines[0], "Short")
	switchAt := strings.Index(lines[1], "Switch")
	if shortAt < 0 || switchAt < 0 {
		t.Fatalf("descriptions missing from rows: %q", lines)
	}
	shortColumn := lipgloss.Width(lines[0][:shortAt])
	switchColumn := lipgloss.Width(lines[1][:switchAt])
	if shortColumn != switchColumn {
		t.Fatalf("descriptions should align, Short at column %d Switch at column %d in %q", shortColumn, switchColumn, lines)
	}
	if got := lipgloss.Width(lines[1]); got > 32 {
		t.Fatalf("row width = %d, want <= 32: %q", got, lines[1])
	}
	if !strings.Contains(lines[1], "…") {
		t.Fatalf("long description should truncate with ellipsis, got %q", lines[1])
	}
}

func TestSelectableListMarksOnlySelectedRow(t *testing.T) {
	rendered := renderSelectableList(selectableListOptions{
		Items: []selectableListItem{
			{Label: "/help", Description: "Show help"},
			{Label: "/model", Description: "Switch model"},
			{Label: "/quit", Description: "Quit"},
		},
		Selected:   1,
		Width:      48,
		MaxVisible: 8,
	})

	lines := strings.Split(plainRender(t, rendered), "\n")
	selected := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "❯ ") {
			selected++
			if !strings.Contains(line, "/model") {
				t.Fatalf("selected marker is on wrong row: %q", line)
			}
		}
	}
	if selected != 1 {
		t.Fatalf("selected marker count = %d, want 1 in %q", selected, lines)
	}
}
