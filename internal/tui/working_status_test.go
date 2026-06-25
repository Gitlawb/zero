package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

// TestWorkingPlanLine: the working indicator's second line carries the plan's
// done/total and the in-progress step while a plan is active, and is empty when
// there's no plan or the plan is complete.
func TestWorkingPlanLine(t *testing.T) {
	m := model{now: time.Now}
	if got := m.workingPlanLine(); got != "" {
		t.Errorf("no plan: want empty, got %q", got)
	}

	m.plan.steps = []planStep{
		{content: "Research the topic", status: "completed"},
		{content: "Add product catalog", status: "in_progress"},
		{content: "Write the docs", status: "pending"},
	}
	got := plainRender(t, m.workingPlanLine())
	if !strings.Contains(got, "plan 1/3") {
		t.Errorf("active plan line missing count: %q", got)
	}
	if !strings.Contains(got, "Add product catalog") {
		t.Errorf("active plan line missing current step: %q", got)
	}

	for i := range m.plan.steps {
		m.plan.steps[i].status = "completed"
	}
	if got := m.workingPlanLine(); got != "" {
		t.Errorf("complete plan: want empty, got %q", got)
	}
}

// TestHiddenPlumbingToolsSkippedFromTranscript: the plumbing tools (update_plan,
// tool_search) render nothing — their call AND result rows are dropped; real
// work tools still render.
func TestHiddenPlumbingToolsSkippedFromTranscript(t *testing.T) {
	rows := []transcriptRow{
		{kind: rowToolCall, tool: "update_plan", id: "c1", runID: 1},
		{kind: rowToolResult, tool: "update_plan", id: "c1", runID: 1, text: "10 steps · 2 done"},
		{kind: rowToolCall, tool: "tool_search", id: "c2", runID: 1},
		{kind: rowToolResult, tool: "tool_search", id: "c2", runID: 1, text: "select:swarm_spawn,…"},
		{kind: rowToolResult, tool: "bash", id: "c3", runID: 1, text: "ok"},
	}
	rc := buildRowContext(rows)
	for _, i := range []int{0, 1, 2, 3} {
		if !rc.skip(rows[i]) {
			t.Errorf("plumbing row %d (%s/%v) should be skipped", i, rows[i].tool, rows[i].kind)
		}
	}
	if rc.skip(rows[4]) {
		t.Error("a normal tool result (bash) must not be skipped")
	}

	// A FAILED plumbing result must still render — its error has to surface.
	failed := transcriptRow{kind: rowToolResult, tool: "update_plan", id: "c9", runID: 1, status: tools.StatusError, text: "tool result: update_plan error boom"}
	if buildRowContext([]transcriptRow{failed}).skip(failed) {
		t.Error("a failed plumbing result must NOT be skipped (the error must show)")
	}

	if !isHiddenPlumbingTool("update_plan") || !isHiddenPlumbingTool("tool_search") {
		t.Error("update_plan and tool_search must be hidden plumbing")
	}
	if isHiddenPlumbingTool("write_file") || isHiddenPlumbingTool("web_search") {
		t.Error("real work tools must NOT be hidden")
	}
}

// TestQuietGenerationHint: after a silent stretch during an active run, the
// working line shows a "still generating…" cue with an advancing timer; while
// output is flowing (recent activity) or when idle, it stays empty.
func TestQuietGenerationHint(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	m := model{now: func() time.Time { return base.Add(30 * time.Second) }}
	m.activeRunID = 7
	m.turnStartedAt = base

	// Recently active -> no hint.
	m.lastStreamActivity = base.Add(28 * time.Second)
	if got := m.quietGenerationHint(); got != "" {
		t.Errorf("recent activity: want no hint, got %q", got)
	}

	// Quiet for >= the threshold -> a "still generating…" cue appears, and shows
	// up on the rendered working line.
	m.lastStreamActivity = base.Add(5 * time.Second) // 25s quiet at now
	if got := m.quietGenerationHint(); !strings.Contains(got, "still generating") {
		t.Errorf("quiet stretch: want a still-generating hint, got %q", got)
	}
	if line := plainRender(t, m.workingStatusLine()); !strings.Contains(line, "still generating") {
		t.Errorf("working line should carry the quiet hint, got %q", line)
	}

	// No active run -> never a hint.
	idle := m
	idle.activeRunID = 0
	if got := idle.quietGenerationHint(); got != "" {
		t.Errorf("idle: want no hint, got %q", got)
	}
}

// TestQuietHintHiddenWhenSidebarShowsIt: when the context sidebar is up (it
// carries the "generating…" pulse in ACTIVITY), the working line must NOT also
// show the hint — it appears in exactly one place.
func TestQuietHintHiddenWhenSidebarShowsIt(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	m := sidebarTestModel() // alt-screen + a plan -> sidebar active
	m.now = func() time.Time { return base.Add(30 * time.Second) }
	m.activeRunID = 7
	m.turnStartedAt = base
	m.lastStreamActivity = base.Add(2 * time.Second) // 28s quiet
	if !m.sidebarActive() {
		t.Fatal("precondition: sidebar should be active for this model")
	}
	if line := plainRender(t, m.workingStatusLine()); strings.Contains(line, "still generating") {
		t.Errorf("working line must NOT duplicate the hint when the sidebar shows it:\n%s", line)
	}
	if act := plainRender(t, strings.Join(m.sidebarActivityLines(sidebarWidth(m.width), 10), "\n")); !strings.Contains(act, "generating") {
		t.Errorf("the sidebar ACTIVITY should carry the generating pulse instead:\n%s", act)
	}
}

func TestFormatWorkingElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		0:                 "0s",
		8 * time.Second:   "8s",
		59 * time.Second:  "59s",
		64 * time.Second:  "1m04s",
		125 * time.Second: "2m05s",
		-3 * time.Second:  "0s",
	}
	for d, want := range cases {
		if got := formatWorkingElapsed(d); got != want {
			t.Errorf("formatWorkingElapsed(%s) = %q, want %q", d, got, want)
		}
	}
}

// The key fix: the live working line (spinner + verb + elapsed) is shown even
// AFTER partial text has streamed, so an upstream stall never looks frozen.
func TestInterimBlockShowsWorkingLineWithStreamedText(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.width = 100
	base := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(12 * time.Second) }
	m.turnStartedAt = base
	m.streamingText = "partial answer so far"

	got := plainRender(t, m.interimBlock(96))
	if !strings.Contains(got, "partial answer so far") {
		t.Fatalf("interim block should keep the streamed text:\n%s", got)
	}
	if !strings.Contains(got, "12s") {
		t.Fatalf("interim block should show live elapsed (12s) below the text:\n%s", got)
	}
	if !strings.Contains(got, "Working") {
		t.Fatalf("interim block should show the liveness label:\n%s", got)
	}
}

// The working line carries a live token estimate ("↑ <n> tok") that climbs as
// the model streams, replacing the old static scroll figure.
func TestWorkingTokenIndicatorEstimatesFromStreamedRunes(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	// Always visible during a run, starting at zero so the working line never
	// drops the counter (the bug: it blinked out during the initial think).
	if got := m.workingTokenIndicator(); !strings.Contains(got, "↑") || !strings.Contains(got, "0 tok") {
		t.Fatalf("at turn start the counter should read like ↑ 0 tok, got %q", got)
	}
	m.turnStreamedRunes = 4000 // ~1000 tokens at ~4 chars/token
	got := m.workingTokenIndicator()
	for _, want := range []string{"↑", "tok", "1K"} {
		if !strings.Contains(got, want) {
			t.Fatalf("indicator = %q, want it to contain %q", got, want)
		}
	}
}

// The estimate must keep climbing across the per-segment buffer clears (a tool
// call wipes streamingText/Reasoning) — turnStreamedRunes accumulates over the
// whole turn, so the counter never snaps back to zero mid-turn.
func TestWorkingTokenIndicatorAccumulatesAcrossSegmentClears(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m = m.beginRun(nil)
	rid := m.activeRunID

	updated, _ := m.Update(agentReasoningMsg{runID: rid, delta: strings.Repeat("a", 40)})
	m = updated.(model)
	afterReasoning := m.turnStreamedRunes
	if afterReasoning == 0 {
		t.Fatal("reasoning deltas should accumulate streamed runes")
	}

	// Simulate the segment boundary that clears the live buffers, then stream
	// answer text in the next segment.
	m.streamingReasoning = ""
	m.streamingText = ""
	updated, _ = m.Update(agentTextMsg{runID: rid, delta: strings.Repeat("b", 40)})
	m = updated.(model)

	if m.turnStreamedRunes <= afterReasoning {
		t.Fatalf("token estimate must keep climbing across the buffer clear: before=%d after=%d", afterReasoning, m.turnStreamedRunes)
	}

	// The climbing figure must actually reach the rendered working line.
	if line := plainRender(t, m.workingStatusLine()); !strings.Contains(line, "tok") {
		t.Fatalf("working status line should carry the live token counter, got %q", line)
	}

	// A fresh turn resets the accumulator to zero.
	m = m.beginRun(nil)
	if m.turnStreamedRunes != 0 {
		t.Fatalf("beginRun should reset the per-turn token estimate, got %d", m.turnStreamedRunes)
	}
}

func TestPreviewTail(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"short", 20, "short"},
		{"exactlyten", 10, "exactlyten"},
		{"abcdefghijklmnop", 6, "…lmnop"}, // tail with leading ellipsis
		{"", 8, ""},
	}
	for _, c := range cases {
		if got := previewTail(c.in, c.width); got != c.want {
			t.Errorf("previewTail(%q,%d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

// The fix: during a think (no answer text yet) the streaming reasoning TAIL is
// shown beneath the working line, so the screen shows live, changing content.
func TestInterimBlockShowsReasoningPreviewWhileThinking(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.width = 100
	base := time.Date(2026, 6, 18, 23, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(90 * time.Second) }
	m.turnStartedAt = base
	m.streamingReasoning = "analyzing the layout\nthe patch was corrupt so re-planning the css edits"
	m.streamingText = "" // thinking phase: no answer yet

	got := plainRender(t, m.interimBlock(96))
	if !strings.Contains(got, "re-planning the css edits") {
		t.Fatalf("expected the live reasoning tail in the preview:\n%s", got)
	}
	if !strings.Contains(got, "1m30s") {
		t.Fatalf("expected the working-line elapsed clock:\n%s", got)
	}
}

// When the reasoning block is EXPANDED, the full body already shows — the
// collapsed tail preview must NOT be duplicated.
func TestInterimBlockNoPreviewWhenReasoningExpanded(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.width = 100
	m.now = func() time.Time { return time.Date(2026, 6, 18, 23, 0, 0, 0, time.UTC) }
	m.streamingReasoningExpanded = true
	m.streamingReasoning = "only line of reasoning here"
	m.streamingText = ""
	got := plainRender(t, m.interimBlock(96))
	if strings.Count(got, "only line of reasoning here") != 1 {
		t.Fatalf("reasoning should appear exactly once when expanded (no preview dup):\n%s", got)
	}
}
