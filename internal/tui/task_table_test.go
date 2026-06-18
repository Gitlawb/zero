package tui

import (
	"testing"
	"time"
)

func TestTaskTableToggle(t *testing.T) {
	var ts taskTableState
	if ts.visible {
		t.Error("task table should start hidden")
	}
	ts.toggle()
	if !ts.visible {
		t.Error("task table should be visible after toggle")
	}
	ts.toggle()
	if ts.visible {
		t.Error("task table should be hidden after second toggle")
	}
}

func TestTaskTableShowHide(t *testing.T) {
	var ts taskTableState
	ts.show()
	if !ts.visible {
		t.Error("task table should be visible after show")
	}
	ts.hide()
	if ts.visible {
		t.Error("task table should be hidden after hide")
	}
}

func TestTaskTableMoveCursor(t *testing.T) {
	var ts taskTableState
	ts.show()

	// With 3 specialists, cursor starts at 0
	ts.moveCursor(1, 3)
	if ts.cursor != 1 {
		t.Errorf("cursor = %d, want 1", ts.cursor)
	}
	ts.moveCursor(1, 3)
	if ts.cursor != 2 {
		t.Errorf("cursor = %d, want 2", ts.cursor)
	}
	// Clamp at max
	ts.moveCursor(1, 3)
	if ts.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (clamped)", ts.cursor)
	}
	// Move back down
	ts.moveCursor(-1, 3)
	if ts.cursor != 1 {
		t.Errorf("cursor = %d, want 1", ts.cursor)
	}
	// Clamp at 0
	ts.moveCursor(-5, 3)
	if ts.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (clamped)", ts.cursor)
	}
}

func TestTaskTableSelectedSessionID(t *testing.T) {
	var ts taskTableState
	specialists := []specialistInfo{
		{childSessionID: "s1", name: "worker"},
		{childSessionID: "s2", name: "explorer"},
	}

	ts.cursor = 0
	if id := ts.selectedSessionID(specialists); id != "s1" {
		t.Errorf("selectedSessionID = %q, want s1", id)
	}

	ts.cursor = 1
	if id := ts.selectedSessionID(specialists); id != "s2" {
		t.Errorf("selectedSessionID = %q, want s2", id)
	}

	// Empty list
	ts.cursor = 0
	if id := ts.selectedSessionID(nil); id != "" {
		t.Errorf("selectedSessionID with empty list = %q, want empty", id)
	}
}

func TestTaskTableHeight(t *testing.T) {
	if h := taskTableHeight(0); h != 6 {
		t.Errorf("taskTableHeight(0) = %d, want 6", h)
	}
	if h := taskTableHeight(3); h != 9 {
		t.Errorf("taskTableHeight(3) = %d, want 9", h)
	}
	if h := taskTableHeight(10); h != 16 {
		t.Errorf("taskTableHeight(10) = %d, want 16", h)
	}
}

func TestTruncateColumn(t *testing.T) {
	// Short string: padded to width
	if got := truncateColumn("short", 10); got != "short     " {
		t.Errorf("truncateColumn short = %q, want 'short     '", got)
	}
	// Exact width
	if got := truncateColumn("exactlyten", 10); got != "exactlyten" {
		t.Errorf("truncateColumn exact = %q, want 'exactlyten'", got)
	}
	// Too long: truncated with ellipsis prefix showing tail
	got := truncateColumn("abcdefghijklmnop", 6)
	if got != "…lmnop" {
		t.Errorf("truncateColumn long = %q, want '…lmnop'", got)
	}
	// Zero width: return as-is
	if got := truncateColumn("test", 0); got != "test" {
		t.Errorf("truncateColumn zero width = %q, want 'test'", got)
	}
}

func TestRenderTaskTableEmpty(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 80
	m.taskTable.show()

	got := m.renderTaskTable(80)
	if got == "" {
		t.Fatal("expected non-empty render for empty task table")
	}
}

func TestRenderTaskTableWithSpecialists(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 100
	m.taskTable.show()

	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now.Add(18 * time.Second) }

	m.specialists.start("worker", "fix oauth tests", "s1", now)
	m.specialists.start("explorer", "map providers", "s2", now.Add(5*time.Second))
	m.specialists.complete("s2", specialistCompleted, 0, "", now.Add(42*time.Second))

	got := m.renderTaskTable(100)
	if got == "" {
		t.Fatal("expected non-empty task table render")
	}
}
