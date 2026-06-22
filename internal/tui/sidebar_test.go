package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func sidebarTestModel() model {
	m := newModel(context.Background(), Options{ProviderName: "test-provider", ModelName: "test-model"})
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	// Real conversation content so the home-screen gate doesn't suppress the
	// sidebar (it stays single-column until the transcript has non-welcome rows).
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
	return m
}

func TestSidebarWidthClampsAndSuppresses(t *testing.T) {
	if got := sidebarWidth(40); got != 0 {
		t.Fatalf("sidebarWidth(40) = %d, want 0 (too narrow for a second column)", got)
	}
	if got := sidebarWidth(100); got < sidebarMinWidth || got > sidebarMaxWidth {
		t.Fatalf("sidebarWidth(100) = %d, want within [%d,%d]", got, sidebarMinWidth, sidebarMaxWidth)
	}
	if got := sidebarWidth(400); got != sidebarMaxWidth {
		t.Fatalf("sidebarWidth(400) = %d, want clamped to %d", got, sidebarMaxWidth)
	}
}

func TestSidebarActiveGating(t *testing.T) {
	m := sidebarTestModel()
	if !m.sidebarActive() {
		t.Fatalf("expected sidebar active for wide alt-screen model")
	}

	// Home/welcome screen (no real conversation yet): single column.
	home := m
	home.transcript = nil
	if home.sidebarActive() {
		t.Fatalf("sidebar should be inactive on the empty home screen")
	}

	// Too narrow: single column only.
	narrow := m
	narrow.width = 50
	if narrow.sidebarActive() {
		t.Fatalf("sidebar should be inactive on a narrow terminal")
	}

	// Inline (non-alt-screen) mode keeps the legacy single-column layout.
	inline := m
	inline.altScreen = false
	if inline.sidebarActive() {
		t.Fatalf("sidebar should be inactive in inline mode")
	}

	// Subchat drill-in owns the full width.
	sub := m
	sub.subchat.active = true
	if sub.sidebarActive() {
		t.Fatalf("sidebar should be inactive during subchat drill-in")
	}
}

func TestSidebarToggleHidesAndShows(t *testing.T) {
	m := sidebarTestModel()
	if !m.sidebarActive() || !m.sidebarAvailable() {
		t.Fatal("sidebar should be active and available for the test model")
	}

	// Ctrl+B hide preference suppresses the sidebar even though it's available.
	m.sidebarHidden = true
	if m.sidebarActive() {
		t.Fatal("sidebar should be inactive when hidden by the user")
	}
	if !m.sidebarAvailable() {
		t.Fatal("sidebarAvailable must ignore the hide preference (so Ctrl+B can re-show)")
	}
	// Hidden → the chat reflows to full width.
	if got, want := m.chatColumnWidth(), chatWidth(m.width); got != want {
		t.Fatalf("hidden sidebar: chat width = %d, want full %d", got, want)
	}

	// Toggling back restores the two-column layout.
	m.sidebarHidden = false
	if !m.sidebarActive() {
		t.Fatal("sidebar should be active again after un-hiding")
	}
}

func TestChatColumnWidthLeavesRoomForSidebar(t *testing.T) {
	m := sidebarTestModel()
	chatW := m.chatColumnWidth()
	sidebarW := sidebarWidth(m.width)
	if chatW+3+sidebarW != m.width {
		t.Fatalf("chat(%d) + divider(3) + sidebar(%d) = %d, want total width %d",
			chatW, sidebarW, chatW+3+sidebarW, m.width)
	}

	// When the sidebar is inactive, chat width is the full chat width.
	narrow := m
	narrow.width = 50
	if got := narrow.chatColumnWidth(); got != chatWidth(narrow.width) {
		t.Fatalf("narrow chatColumnWidth = %d, want full chatWidth %d", got, chatWidth(narrow.width))
	}
}

func TestRenderContextSidebarDimensions(t *testing.T) {
	m := sidebarTestModel()
	width := sidebarWidth(m.width)
	const height = 20
	lines := m.renderContextSidebar(width, height)
	if len(lines) != height {
		t.Fatalf("sidebar produced %d lines, want exactly %d", len(lines), height)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != width {
			t.Fatalf("sidebar line %d width = %d, want exactly %d", i, w, width)
		}
	}
	// Section headers and the token floor should be present.
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "AGENTS") {
		t.Fatalf("sidebar missing AGENTS header:\n%s", plain)
	}
	if !strings.Contains(plain, "PLAN") {
		t.Fatalf("sidebar missing PLAN header:\n%s", plain)
	}
	if !strings.Contains(plain, "tokens") {
		t.Fatalf("sidebar missing token floor:\n%s", plain)
	}
}

func TestSidebarShowsSpawnedAgents(t *testing.T) {
	m := sidebarTestModel()
	now := time.Now()
	// One running subagent with live tool activity, one completed.
	m.specialists.start("explorer", "map the codebase", "sess-1", now)
	m.specialists.setCurrentTool("sess-1", "grep", "auth")
	m.specialists.incrementToolCount("sess-1")
	m.specialists.start("reviewer", "review diff", "sess-2", now)
	m.specialists.complete("sess-2", specialistCompleted, 0, "", now)

	width := sidebarWidth(m.width)
	plain := stripSidebar(m.sidebarAgentLines(width))
	if !strings.Contains(plain, "explorer") {
		t.Fatalf("running subagent name missing:\n%s", plain)
	}
	if !strings.Contains(plain, "reviewer") {
		t.Fatalf("completed subagent name missing:\n%s", plain)
	}
	// The running subagent surfaces its live working detail (current tool).
	if !strings.Contains(plain, "grep") {
		t.Fatalf("running subagent working detail missing:\n%s", plain)
	}
	// Header shows the total agent count.
	hdr := stripSidebar([]string{m.sidebarAgentHeader(width)})
	if !strings.Contains(hdr, "AGENTS") || !strings.Contains(hdr, "2") {
		t.Fatalf("agent header should show AGENTS 2, got: %s", hdr)
	}
}

func TestSidebarShowsSwarmSpawnedAgents(t *testing.T) {
	m := sidebarTestModel()
	// Each swarm member is a CALL row (detail = the task briefing, as argHint
	// would produce it) paired with its following RESULT row (yielding the id).
	// The sidebar names the agent by the task, not the opaque "subagent-N".
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "audit the auth flow"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default."},
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "write integration tests"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-2 on team default."},
		// Duplicate id (a re-report): deduped, keeps the first member's name.
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "audit the auth flow"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default."},
	)
	agents := m.swarmSpawnedAgents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 unique swarm members, got %v", agents)
	}
	if agents[0].id != "subagent-1" || agents[0].name != "audit auth" {
		t.Fatalf("member 0 = %+v, want id subagent-1 named 'audit auth' (short task name)", agents[0])
	}
	if agents[1].id != "subagent-2" || agents[1].name != "write integration" {
		t.Fatalf("member 1 = %+v, want id subagent-2 named 'write integration' (short task name)", agents[1])
	}
	width := sidebarWidth(m.width)
	plain := stripSidebar(m.sidebarAgentLines(width))
	// The sidebar shows the short task-derived names, not the raw ids or full task.
	if !strings.Contains(plain, "audit auth") || !strings.Contains(plain, "write integration") {
		t.Fatalf("swarm member short task names missing from sidebar:\n%s", plain)
	}
	hdr := stripSidebar([]string{m.sidebarAgentHeader(width)})
	if !strings.Contains(hdr, "AGENTS") || !strings.Contains(hdr, "2") {
		t.Fatalf("header should show AGENTS 2, got: %s", hdr)
	}
}

// TestSwarmSpawnedAgentFallsBackToID covers a result row with no preceding call
// row (e.g. a resumed transcript that dropped the call): the member is still
// shown, named by its id.
func TestSwarmSpawnedAgentFallsBackToID(t *testing.T) {
	m := sidebarTestModel()
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-9 on team default."},
	)
	agents := m.swarmSpawnedAgents()
	if len(agents) != 1 || agents[0].id != "subagent-9" || agents[0].name != "subagent-9" {
		t.Fatalf("expected one member named by id, got %+v", agents)
	}
}

func TestShortTaskName(t *testing.T) {
	cases := map[string]string{
		"Explore the repository structure and summarize": "Explore repository",
		"Review the current git branch":                  "Review current",
		"Check for any TODOs, FIXMEs":                    "Check TODOs",
		"Provide a high-level overview":                  "Provide high-level",
		"single":                                         "single",
		"":                                               "",
	}
	for task, want := range cases {
		if got := shortTaskName(task); got != want {
			t.Errorf("shortTaskName(%q) = %q, want %q", task, got, want)
		}
	}
}

func TestSwarmAgentsLingerThenDisappearWhenDone(t *testing.T) {
	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	m := sidebarTestModel()
	m.now = func() time.Time { return base }
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "explore repo"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned teammate as task teammate-1 on team default."},
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "review branch"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned teammate as task teammate-2 on team default."},
	)
	if got := len(m.swarmSpawnedAgents()); got != 2 {
		t.Fatalf("expected 2 live members before any status report, got %d", got)
	}
	// teammate-1 reported done, teammate-2 still running.
	m.transcript = append(m.transcript, transcriptRow{
		kind: rowToolResult, tool: "swarm_status",
		detail: "Swarm status (team default): 2 task(s)\n– teammate-1 [done] (cyan) explore repo\n– teammate-2 [running] (blue) review branch",
	})
	// Freshly done (not yet stamped by the tick): teammate-1 LINGERS (finishing).
	agents := m.swarmSpawnedAgents()
	if len(agents) != 2 {
		t.Fatalf("a freshly-done member should linger, got %d: %+v", len(agents), agents)
	}
	var done *swarmAgent
	for i := range agents {
		if agents[i].id == "teammate-1" {
			done = &agents[i]
		}
	}
	if done == nil || !done.finishing {
		t.Fatalf("teammate-1 should be finishing (lingering), got %+v", agents)
	}

	// The spinner tick stamps the done time; once the linger window elapses the
	// member is removed.
	m.stampSwarmDone()
	if _, ok := m.swarmDoneAt["teammate-1"]; !ok {
		t.Fatal("stampSwarmDone should record the finished member")
	}
	m.swarmDoneAt["teammate-1"] = base.Add(-2 * sidebarAgentLinger) // past the window
	agents = m.swarmSpawnedAgents()
	if len(agents) != 1 || agents[0].id != "teammate-2" {
		t.Fatalf("after the linger the done member should be gone; got %+v", agents)
	}
}

func TestSidebarHidesNotFoundSpecialistMisroutes(t *testing.T) {
	m := sidebarTestModel()
	now := time.Now()
	// A real running specialist + a failed tool-misroute (a swarm tool name called
	// as a specialist → "specialist not found"), which should be filtered out.
	m.specialists.start("worker", "build frontend", "sess-real", now)
	m.specialists.start("swarm_send", "coordinate", "sess-bogus", now)
	m.specialists.complete("sess-bogus", specialistError, 0, `specialist "swarm_send" not found`, now)

	got := m.sidebarSpecialists()
	if len(got) != 1 || got[0].name != "worker" {
		t.Fatalf("not-found misroute should be filtered; want only worker, got %+v", got)
	}
	plain := stripSidebar(m.sidebarAgentLines(sidebarWidth(m.width)))
	if strings.Contains(plain, "swarm_send") {
		t.Fatalf("bogus swarm_send specialist should not appear:\n%s", plain)
	}
	if !strings.Contains(plain, "worker") {
		t.Fatalf("real worker specialist should still appear:\n%s", plain)
	}
}

func TestSidebarPlanReflectsState(t *testing.T) {
	m := sidebarTestModel()
	m.plan.steps = []planStep{
		{content: "read code", status: "completed"},
		{content: "refactor auth", status: "in_progress"},
		{content: "run tests", status: "pending"},
	}
	header := plainRender(t, m.sidebarPlanHeader(40))
	if !strings.Contains(header, "PLAN") || !strings.Contains(header, "1/3") {
		t.Fatalf("plan header = %q, want PLAN with 1/3 count", header)
	}
	lines := m.sidebarPlanLines(40)
	if len(lines) != 3 {
		t.Fatalf("plan lines = %d, want 3", len(lines))
	}
	joined := stripSidebar(lines)
	if !strings.Contains(joined, "✓") || !strings.Contains(joined, "•") || !strings.Contains(joined, "○") {
		t.Fatalf("plan lines missing status glyphs:\n%s", joined)
	}
}

func TestJoinColumnsAligns(t *testing.T) {
	chat := []string{"hello", "world", "third row that is longer"}
	sidebar := []string{"A", "B"}
	const chatW, sidebarW = 12, 6
	rows := joinColumns(chat, sidebar, chatW, sidebarW)
	if len(rows) != 3 {
		t.Fatalf("joined %d rows, want max(3,2)=3", len(rows))
	}
	want := chatW + 3 + sidebarW // " │ " padded divider
	for i, row := range rows {
		if w := lipgloss.Width(row); w != want {
			t.Fatalf("row %d width = %d, want %d", i, w, want)
		}
	}
}

func TestTwoColumnTranscriptViewWidth(t *testing.T) {
	m := sidebarTestModel()
	out := m.twoColumnTranscriptView()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("two-column view = %d lines, want terminal height %d", len(lines), m.height)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != m.width {
			t.Fatalf("two-column row %d width = %d, want full width %d", i, w, m.width)
		}
	}
}

// stripSidebar joins sidebar lines and strips ANSI for content assertions.
func stripSidebar(lines []string) string {
	return ansiPattern.ReplaceAllString(strings.Join(lines, "\n"), "")
}
