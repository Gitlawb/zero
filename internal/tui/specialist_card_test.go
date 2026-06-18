package tui

import (
	"testing"
	"time"
)

func TestSpecialistTrackerStartAndComplete(t *testing.T) {
	var tracker specialistTracker
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tracker.start("worker", "fix oauth tests", "session-123", now)

	all := tracker.all()
	if len(all) != 1 {
		t.Fatalf("expected 1 specialist, got %d", len(all))
	}
	if all[0].name != "worker" {
		t.Errorf("name = %q, want worker", all[0].name)
	}
	if all[0].status != specialistRunning {
		t.Errorf("status = %v, want specialistRunning", all[0].status)
	}
	if !tracker.hasRunning() {
		t.Error("tracker should have running specialist")
	}

	tracker.complete("session-123", specialistCompleted, 0, "", now.Add(45*time.Second))

	info, ok := tracker.getBySessionID("session-123")
	if !ok {
		t.Fatal("specialist not found after complete")
	}
	if info.status != specialistCompleted {
		t.Errorf("status = %v, want specialistCompleted", info.status)
	}
	if tracker.hasRunning() {
		t.Error("tracker should not have running specialist after completion")
	}
}

func TestSpecialistTrackerIncrementToolCount(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()

	tracker.start("worker", "task", "s1", now)
	tracker.incrementToolCount("s1")
	tracker.incrementToolCount("s1")
	tracker.incrementToolCount("s1")

	info, _ := tracker.getBySessionID("s1")
	if info.toolCount != 3 {
		t.Errorf("toolCount = %d, want 3", info.toolCount)
	}
}

func TestSpecialistTrackerAddTokens(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()

	tracker.start("worker", "task", "s1", now)
	tracker.addTokens("s1", 1000)
	tracker.addTokens("s1", 500)

	info, _ := tracker.getBySessionID("s1")
	if info.tokenCount != 1500 {
		t.Errorf("tokenCount = %d, want 1500", info.tokenCount)
	}
}

func TestSpecialistTrackerClear(t *testing.T) {
	var tracker specialistTracker
	tracker.start("worker", "task", "s1", time.Now())
	tracker.clear()

	if len(tracker.all()) != 0 {
		t.Error("tracker should be empty after clear")
	}
}

func TestSpecialistTrackerDuplicateStart(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()

	tracker.start("worker", "task1", "s1", now)
	tracker.start("worker", "task2", "s1", now.Add(5*time.Second))

	all := tracker.all()
	if len(all) != 1 {
		t.Errorf("duplicate start should update, not add: got %d", len(all))
	}
	if all[0].description != "task2" {
		t.Errorf("description = %q, want task2", all[0].description)
	}
}

func TestSpecialistStatusString(t *testing.T) {
	tests := []struct {
		status specialistStatus
		want   string
	}{
		{specialistRunning, "running"},
		{specialistCompleted, "completed"},
		{specialistError, "error"},
	}
	for _, tt := range tests {
		if got := specialistStatusString(tt.status); got != tt.want {
			t.Errorf("specialistStatusString(%v) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestParseSpecialistStatus(t *testing.T) {
	tests := []struct {
		input string
		want  specialistStatus
	}{
		{"running", specialistRunning},
		{"completed", specialistCompleted},
		{"error", specialistError},
		{"unknown", specialistError},
		{"", specialistError},
	}
	for _, tt := range tests {
		if got := parseSpecialistStatus(tt.input); got != tt.want {
			t.Errorf("parseSpecialistStatus(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{100, "100"},
		{1000, "1,000"},
		{1840, "1,840"},
		{5210, "5,210"},
		{1000000, "1,000,000"},
	}
	for _, tt := range tests {
		if got := formatTokenCount(tt.input); got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatSpecialistElapsed(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "1s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{65 * time.Second, "1m5s"},
		{125 * time.Second, "2m5s"},
	}
	for _, tt := range tests {
		if got := formatSpecialistElapsed(tt.d); got != tt.want {
			t.Errorf("formatSpecialistElapsed(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestRenderSpecialistCard(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 80
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now.Add(18 * time.Second) }

	info := specialistInfo{
		name:           "worker",
		description:    "fix oauth tests",
		childSessionID: "s1",
		status:         specialistRunning,
		startedAt:      now,
		toolCount:      3,
		tokenCount:     1840,
	}

	got := m.renderSpecialistCard(info, 80)
	if got == "" {
		t.Fatal("expected non-empty specialist card")
	}
}

func TestParseTaskCallArgs(t *testing.T) {
	name, desc := parseTaskCallArgs(`{"name":"worker","description":"fix tests"}`)
	if name != "worker" {
		t.Errorf("name = %q, want worker", name)
	}
	if desc != "fix tests" {
		t.Errorf("description = %q, want 'fix tests'", desc)
	}

	// Fall back to prompt when description is missing
	name2, desc2 := parseTaskCallArgs(`{"name":"explorer","prompt":"map the codebase"}`)
	if name2 != "explorer" {
		t.Errorf("name = %q, want explorer", name2)
	}
	if desc2 != "map the codebase" {
		t.Errorf("description = %q, want 'map the codebase'", desc2)
	}
}
