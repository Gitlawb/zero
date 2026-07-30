package tui

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

// TestSidebarActivityLines: the ACTIVITY feed is a bounded, newest-first list of
// recent completed work (stripped of the "tool result:" prefix), with a live
// "generating…" pulse when the run is active and quiet.
func TestSidebarActivityLines(t *testing.T) {
	m := model{now: time.Now}
	m.transcript = []transcriptRow{
		{kind: rowToolCall, tool: "bash", id: "c1", arg: "mkdir -p boutique-site"},
		{kind: rowToolResult, tool: "bash", id: "c1", status: tools.StatusOK, text: "tool result: bash ok Command completed with no output."},
		{kind: rowToolResult, tool: "write_file", id: "c2", status: tools.StatusOK, text: "tool result: write_file ok Created styles.css (1045 lines)."},
		{kind: rowAssistant, text: "Now the JS…"}, // ignored: not a work result
	}
	joined := plainRender(t, strings.Join(m.sidebarActivityLines(40, 10), "\n"))

	wfIdx := strings.Index(joined, "Created styles.css (1045 lines).")
	bashIdx := strings.Index(joined, "mkdir -p boutique-site")
	if wfIdx < 0 || bashIdx < 0 {
		t.Fatalf("activity should list the write_file summary and the bash command:\n%s", joined)
	}
	if wfIdx > bashIdx {
		t.Errorf("activity should be newest-first (write_file before bash):\n%s", joined)
	}
	if strings.Contains(joined, "tool result:") {
		t.Errorf("activity must strip the 'tool result:' prefix:\n%s", joined)
	}
	if got := m.sidebarActivityLines(40, 0); got != nil {
		t.Errorf("zero budget: want nil, got %v", got)
	}

	// Active + quiet run -> a live "generating…" pulse.
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	live := model{now: func() time.Time { return base.Add(30 * time.Second) }}
	live.activeRunID = 7
	live.turnStartedAt = base
	live.lastStreamActivity = base.Add(2 * time.Second) // 28s quiet
	if got := plainRender(t, strings.Join(live.sidebarActivityLines(40, 10), "\n")); !strings.Contains(got, "generating") {
		t.Errorf("active+quiet run should show a generating pulse:\n%s", got)
	}
}

func swarmSidebarTestModel(t *testing.T, sessionIDs map[string]string) model {
	t.Helper()
	m := sidebarTestModel()
	m.swarmSessionMap = sessionIDs
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "build the homepage"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default."},
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "build the stylesheet"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-2 on team default."},
	)
	return m
}

func TestSwarmMemberRowCarriesSessionID(t *testing.T) {
	m := swarmSidebarTestModel(t, map[string]string{"subagent-1": "sess-1"})
	agents := m.swarmSpawnedAgents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 members, got %d", len(agents))
	}
	if agents[0].sessionID != "sess-1" {
		t.Fatalf("member 1 should carry its session id, got %q", agents[0].sessionID)
	}
	if agents[1].sessionID != "" {
		t.Fatalf("member 2 has no mapped session yet, got %q", agents[1].sessionID)
	}
}

func TestSidebarAgentSelectablesMapToScreenRows(t *testing.T) {
	m := swarmSidebarTestModel(t, map[string]string{"subagent-1": "sess-1", "subagent-2": "sess-2"})
	sel := m.sidebarAgentSelectables(sidebarWidth(m.width))
	if len(sel) != 2 {
		t.Fatalf("expected 2 selectable member rows, got %d: %+v", len(sel), sel)
	}
	// AGENTS header occupies sidebar index 0, so the two members are at 1 and 2.
	if sel[0].lineOffset != 1 || sel[1].lineOffset != 2 {
		t.Fatalf("selectable offsets = %d,%d, want 1,2", sel[0].lineOffset, sel[1].lineOffset)
	}
	if sel[0].sessionID != "sess-1" || sel[1].sessionID != "sess-2" {
		t.Fatalf("selectable session ids = %q,%q", sel[0].sessionID, sel[1].sessionID)
	}
}

func TestSidebarLineAtMouseHitsMemberRow(t *testing.T) {
	m := swarmSidebarTestModel(t, map[string]string{"subagent-1": "sess-1"})
	// Sidebar starts at screen X = chatColumnWidth + 3 (the " │ " divider); the
	// first member row is at sidebar line 1 → screen Y 1.
	x := m.chatColumnWidth() + 3 + 2
	hit, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, x, 1))
	if !ok || hit.sessionID != "sess-1" {
		t.Fatalf("expected to hit member row (sess-1), got ok=%v hit=%+v", ok, hit)
	}
	// A click in the chat column (left of the divider) must miss the sidebar.
	if _, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, 2, 1)); ok {
		t.Fatal("a click in the chat column should not hit the sidebar")
	}
	// The AGENTS header row (Y 0) is not a clickable member.
	if _, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, x, 0)); ok {
		t.Fatal("the AGENTS header row should not be clickable")
	}
	// A member with no known session (subagent-2 at Y 2) is not clickable.
	if _, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, x, 2)); ok {
		t.Fatal("a member without a session id should not be clickable")
	}
}

func TestSidebarMemberClickRoutesToSubchatDrillIn(t *testing.T) {
	// A real session so the click can actually drill in (not just be "handled").
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "member: build the homepage", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{
		Type:    sessions.EventMessage,
		Payload: map[string]any{"role": "assistant", "content": "member work output"},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	m := swarmSidebarTestModel(t, map[string]string{"subagent-1": session.SessionID})
	m.sessionStore = store
	x := m.chatColumnWidth() + 3 + 2
	next, _, handled := m.handleTranscriptSelectionMouse(testMouseClick(tea.MouseLeft, x, 1))
	if !handled {
		t.Fatal("clicking a clickable member row should be handled")
	}
	// It must actually enter the member's subchat session, not merely consume the click.
	if !next.subchat.active || next.subchat.childSessionID != session.SessionID {
		t.Fatalf("click should drill into member session %q, got active=%v id=%q",
			session.SessionID, next.subchat.active, next.subchat.childSessionID)
	}
}

func TestSwarmSessionsMsgPopulatesMap(t *testing.T) {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(swarmSessionsMsg{sessions: map[string]string{
		"subagent-1": "sess-1", "": "skip", "subagent-2": "",
	}})
	next := updated.(model)
	if next.swarmSessionMap["subagent-1"] != "sess-1" {
		t.Fatalf("expected sess-1, got %q", next.swarmSessionMap["subagent-1"])
	}
	if _, ok := next.swarmSessionMap[""]; ok {
		t.Fatal("an empty task id must be skipped")
	}
	if _, ok := next.swarmSessionMap["subagent-2"]; ok {
		t.Fatal("an empty session id must be skipped")
	}
}

func sidebarTestModel() model {
	m := newModel(context.Background(), Options{ProviderName: "test-provider", ModelName: "test-model"})
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	// Real conversation content so the home-screen gate doesn't suppress the
	// sidebar (it stays single-column until the transcript has non-welcome rows).
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
	// A plan gives the sidebar content so it isn't auto-hidden as empty (the panel
	// only claims a column when there are agents or an active plan). Tests that
	// exercise specific agent/plan states set their own and override this.
	m.plan.steps = []planStep{{content: "wire it up", status: "in_progress"}}
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

// TestSidebarAutoHidesWhenEmpty: with no agents and no active plan the panel
// auto-hides and the chat reclaims the full width; adding a plan or an agent
// brings it back.
func TestSidebarAutoHidesWhenEmpty(t *testing.T) {
	m := sidebarTestModel() // has a plan -> sidebar active
	if !m.sidebarActive() {
		t.Fatal("expected sidebar active when the model has a plan")
	}

	// Clear the only content (the plan) -> empty -> auto-hidden.
	m.plan.steps = nil
	if m.sidebarHasContent() {
		t.Fatal("model should have no sidebar content after clearing the plan")
	}
	if m.sidebarActive() {
		t.Error("sidebar should auto-hide with no agents and no active plan")
	}
	if got, want := m.chatColumnWidth(), chatWidth(m.width); got != want {
		t.Errorf("empty sidebar: chat width = %d, want full %d", got, want)
	}

	// A spawned agent brings the panel back.
	m.specialists.start("explorer", "look around", "sess-x", time.Now())
	if !m.sidebarHasContent() || !m.sidebarActive() {
		t.Error("sidebar should return once an agent spawns")
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
// A finished member stays visible (and clickable) while the run is still in
// flight, so the user can inspect it; only once the turn ends does it drop.
func TestSwarmAgentDropsOnOwnCompletionEvenMidRun(t *testing.T) {
	base := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	m := sidebarTestModel()
	m.now = func() time.Time { return base }
	m.pending = true  // run STILL going — a member must still drop on its OWN completion
	m.activeRunID = 7 // exercise the run-scoped filter with a non-zero id
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "build homepage", runID: 7},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default.", runID: 7},
		transcriptRow{kind: rowToolResult, tool: "swarm_collect", detail: "Results: 1 task(s)\n- subagent-1 [done] build homepage", runID: 7},
	)
	// Just finished, still within the linger window: shows briefly with a fading ✓.
	m.swarmDoneAt = map[string]time.Time{"subagent-1": base.Add(-sidebarAgentLinger / 2)}
	agents := m.swarmSpawnedAgents()
	if len(agents) != 1 {
		t.Fatalf("within the linger window a finished member should still show (fading), got %d: %+v", len(agents), agents)
	}
	if !agents[0].finishing {
		t.Fatalf("a finished member should render done (✓), got %+v", agents[0])
	}

	// Past the linger window: it drops — even though the overall run is still in
	// flight (previously a finished member lingered until the whole turn ended).
	m.swarmDoneAt = map[string]time.Time{"subagent-1": base.Add(-2 * sidebarAgentLinger)}
	if got := len(m.swarmSpawnedAgents()); got != 0 {
		t.Fatalf("a member past its linger window must drop on its own completion mid-run, got %d", got)
	}
}

// Members AND statuses from a previous run must not bleed into a later run, even
// when task ids repeat — both the spawn rows and the swarm_status/collect rows
// are scoped to the active run.
func TestSwarmAgentsScopedToActiveRun(t *testing.T) {
	m := sidebarTestModel()
	m.pending = true
	m.activeRunID = 2
	m.transcript = append(m.transcript,
		// Old run (runID 1): the SAME task id, and a stale "done" status for it.
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "old task", runID: 1},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default.", runID: 1},
		transcriptRow{kind: rowToolResult, tool: "swarm_status", detail: "- subagent-1 [done] old task", runID: 1},
		// Current run (runID 2): the same id is reused and is still running.
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "new task", runID: 2},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default.", runID: 2},
	)
	agents := m.swarmSpawnedAgents()
	if len(agents) != 1 || agents[0].id != "subagent-1" {
		t.Fatalf("only the current run's member should show, got %+v", agents)
	}
	// The stale prior-run "done" status must NOT mark the current member finished.
	if agents[0].finishing || agents[0].state == "done" {
		t.Fatalf("stale prior-run status must not affect the current member: %+v", agents[0])
	}
}

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

// Regression: a swarm_collect that runs WHILE members are still working must not
// clear the AGENTS panel. Previously any swarm_collect result wiped the roster,
// so the sidebar showed "no agents spawned" mid-run even with 4 subagents live.
func TestSwarmAgentsStayVisibleWhileCollectRunsMidFlight(t *testing.T) {
	m := sidebarTestModel()
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "build the homepage"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default."},
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "build the stylesheet"},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-2 on team default."},
		// A collect mid-flight, both members still running.
		transcriptRow{kind: rowToolResult, tool: "swarm_collect",
			detail: "Results for team default: 2 task(s)\n- subagent-1 [running] build the homepage\n- subagent-2 [running] build the stylesheet"},
	)

	agents := m.swarmSpawnedAgents()
	if len(agents) != 2 {
		t.Fatalf("running members must survive a mid-flight swarm_collect, got %d: %+v", len(agents), agents)
	}
	for _, a := range agents {
		if a.finishing {
			t.Fatalf("a running member must not be marked finishing: %+v", a)
		}
		if a.state != "running" {
			t.Fatalf("swarm_collect should set member state to running, got %q for %s", a.state, a.id)
		}
	}

	plain := stripSidebar(m.sidebarAgentLines(sidebarWidth(m.width)))
	if !strings.Contains(plain, "homepage") || !strings.Contains(plain, "stylesheet") {
		t.Fatalf("sidebar should list the running members and their tasks:\n%s", plain)
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

func TestTwoColumnSidebarRemainsFullHeightBesideFooter(t *testing.T) {
	m := sidebarTestModel()
	m.width, m.height = 120, 34
	m.unpricedTokens = 10000
	out := plainRender(t, m.twoColumnTranscriptView())
	lines := strings.Split(out, "\n")

	tokenRow := -1
	composerTop := -1
	for index, line := range lines {
		if strings.Contains(line, "10K tokens") {
			tokenRow = index
		}
		if strings.HasPrefix(line, "╭") {
			composerTop = index
		}
	}
	if tokenRow != len(lines)-1 {
		t.Fatalf("sidebar token summary should remain pinned to the bottom, row=%d last=%d\n%s", tokenRow, len(lines)-1, out)
	}
	if composerTop < 0 {
		t.Fatalf("chat composer missing:\n%s", out)
	}
	composerRunes := []rune(lines[composerTop])
	if len(composerRunes) != m.width || composerRunes[m.chatColumnWidth()-1] != '╮' {
		t.Fatalf("composer should retain the chat-column width, got %q", lines[composerTop])
	}
	for index, line := range lines {
		runes := []rune(line)
		if len(runes) != m.width || runes[m.chatColumnWidth()+1] != '│' {
			t.Fatalf("sidebar divider ended early on row %d: %q", index, line)
		}
	}
}

// stripSidebar joins sidebar lines and strips ANSI for content assertions.
func stripSidebar(lines []string) string {
	return ansiPattern.ReplaceAllString(strings.Join(lines, "\n"), "")
}

// The `/` command palette must NOT collapse the sidebar. It is a centred box
// capped at suggestionPaletteMaxWidth floating over the chat column, not a
// full-screen overlay — suppressing the second column for it dropped the plan
// out of the sidebar and re-rendered it inline at the bottom on every `/`,
// which is at its most disruptive mid-run with a live plan on screen. The
// genuinely full-width overlays must still suppress it.
func TestSidebarSurvivesCommandPalette(t *testing.T) {
	base := func() model {
		m := runningPlanModel(t, 3)
		m.altScreen = true
		m.height = 40
		m.headerPrinted = true
		m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
		return m
	}

	m := base()
	if !m.sidebarActive() {
		t.Fatal("precondition: sidebar should be active for a wide alt-screen model with a plan")
	}

	// `/` palette open: sidebar stays, so the plan keeps its home and the layout
	// does not reflow.
	m.suggestions = []commandSuggestion{{Name: "/model", Desc: "Pick a model."}, {Name: "/plan", Desc: "Show planning mode status."}}
	if !m.suggestionsActive() {
		t.Fatal("precondition: suggestions should be active")
	}
	if !m.sidebarActive() {
		t.Error("command palette must not collapse the sidebar")
	}
	if got := m.renderPinnedPlanPanel(m.chatColumnWidth(), 10); got != "" {
		t.Errorf("plan must stay in the sidebar, not fall back to the pinned panel:\n%s", got)
	}
	// The palette never contends for the sidebar's cells: the two-column path
	// renders it at width = chatColumnWidth, so it is centred inside the chat
	// column and cannot overlap the sidebar.
	chatW := m.chatColumnWidth()
	for _, line := range strings.Split(plainRender(t, m.suggestionOverlay(chatW)), "\n") {
		if w := lipgloss.Width(line); w > chatW {
			t.Errorf("palette line is %d wide, wider than the chat column %d — it would overlap the sidebar", w, chatW)
		}
	}
	// Clicks still belong to the palette, not the sidebar rows beneath it.
	if _, ok := m.sidebarLineAtMouse(tea.MouseClickMsg{Button: tea.MouseLeft}); ok {
		t.Error("sidebar must not take mouse hits while the palette is open")
	}

	// A genuinely full-width overlay still suppresses the sidebar.
	full := base()
	full.picker = &commandPicker{}
	if full.sidebarActive() {
		t.Error("a full-screen picker must still collapse the sidebar")
	}
}

// specialistSidebarModel is a sidebar-active model with one running plan task in
// AGENTS, an update_plan step list in PLAN, and a touched file in FILES — so the
// sections BELOW the agent rows are present and their click offsets are real.
func specialistSidebarModel(t *testing.T, now time.Time) model {
	t.Helper()
	m := sidebarTestModel()
	m.now = func() time.Time { return now }
	m.specialists.start("cfg", "read the config resolver and report how the profile tier merges", "plantask_1", now.Add(-70*time.Second))
	m.specialists.incrementToolCount("plantask_1")
	m.specialists.setTokens("plantask_1", 3400)
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolResult, tool: "write_file", detail: "internal/tui/sidebar.go"})
	if !m.sidebarActive() {
		t.Fatal("sanity check failed: the sidebar must be up for this test")
	}
	return m
}

// A RUNNING agent row is clickable. It is keyed by its card, and a plan task's
// card key is not a session id until the task finishes — so the drill-in has
// nothing to open at the one moment the detail is most wanted.
func TestClickingARunningAgentRowExpandsItInPlace(t *testing.T) {
	now := time.Unix(20000, 0)
	m := specialistSidebarModel(t, now)

	hits := m.sidebarAgentSelectables(sidebarWidth(m.width))
	if len(hits) != 1 {
		t.Fatalf("expected the specialist row to be clickable, got %d hits", len(hits))
	}
	if !hits[0].expands || hits[0].sessionID != "plantask_1" {
		t.Fatalf("specialist hit = %+v, want an in-place expansion keyed by its card", hits[0])
	}

	collapsed := plainRender(t, strings.Join(m.sidebarAgentLines(sidebarWidth(m.width)), "\n"))
	if strings.Contains(collapsed, "config resolver") {
		t.Errorf("the collapsed row must not already show the brief:\n%s", collapsed)
	}

	x0 := m.chatColumnWidth() + 3
	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: x0, Y: hits[0].lineOffset}
	updated, _, handled := m.handleTranscriptSelectionMouse(click)
	if !handled {
		t.Fatal("the click was not handled")
	}
	if updated.expandedAgent != "plantask_1" {
		t.Fatalf("expandedAgent = %q, want the clicked row's card", updated.expandedAgent)
	}
	// IN PLACE, NOT A DRILL-IN. The swarm path on the other side of this branch
	// swaps the whole view for the member's subchat; a card key that is not yet
	// a session would take it there with nothing to open.
	if updated.subchat.active {
		t.Error("expanding a specialist row must not enter the swarm subchat")
	}

	expanded := plainRender(t, strings.Join(updated.sidebarAgentLines(sidebarWidth(m.width)), "\n"))
	// The column is 26 cells at its minimum, so the brief wraps and the spend
	// line truncates — checked against what actually renders, not against a
	// wider terminal's version of it.
	for _, want := range []string{"read the config", "1m10s", "3.4K tok"} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expansion is missing %q:\n%s", want, expanded)
		}
	}

	// Clicking the open row closes it again.
	reclicked, _, _ := updated.handleTranscriptSelectionMouse(click)
	if reclicked.expandedAgent != "" {
		t.Errorf("a second click must close the row, got %q", reclicked.expandedAgent)
	}
}

// THE OFFSET CONSTRAINT. sidebarPlanSelectables and sidebarFileSelectables both
// derive their base from len(sidebarAgentLines), so an expansion that adds rows
// must move every click target below it. Getting this wrong sends a click to a
// different file than the one under the cursor, with nothing to indicate it.
func TestExpandingAnAgentMovesTheClickTargetsBelowIt(t *testing.T) {
	now := time.Unix(20000, 0)
	m := specialistSidebarModel(t, now)
	width := sidebarWidth(m.width)

	// The rendered sidebar is the oracle: whatever a hit points at must be the
	// row it claims, in both states.
	check := func(t *testing.T, m model, label string) {
		t.Helper()
		lines := m.renderContextSidebar(width, m.height)
		for _, hit := range m.sidebarPlanSelectables(width) {
			line := plainRender(t, lines[hit.lineOffset])
			if !strings.Contains(line, m.plan.steps[hit.stepIndex].content) {
				t.Errorf("%s: plan step %d points at %q", label, hit.stepIndex, line)
			}
		}
		for _, hit := range m.sidebarFileSelectables(width) {
			line := plainRender(t, lines[hit.lineOffset])
			if !strings.Contains(line, path.Base(hit.path)) {
				t.Errorf("%s: file hit %q points at %q", label, hit.path, line)
			}
		}
	}

	check(t, m, "collapsed")
	m.expandedAgent = "plantask_1"
	check(t, m, "expanded")
}

// Finishing a task swaps its card id for the child's real session id. A row the
// user had open must not collapse at the exact moment it gains a result.
func TestAnOpenAgentRowSurvivesTheSessionRename(t *testing.T) {
	now := time.Unix(20000, 0)
	m := specialistSidebarModel(t, now)
	m.activeRunID = 1
	m.expandedAgent = "plantask_1"

	updated, _ := m.Update(planTaskDoneMsg{
		runID: 1, taskID: "cfg", cardKey: "plantask_1", dispatched: true,
		sessionID: "specialist_real", status: specialistCompleted, outcome: "succeeded",
	})
	got := updated.(model)
	if got.expandedAgent != "specialist_real" {
		t.Errorf("expandedAgent = %q, want it to follow the rename to the real session", got.expandedAgent)
	}
}

// A cancelled task explains itself, and NOT in red: the user stopped it, and
// colouring their own decision as a fault is what the ⊘ glyph already avoids.
func TestACancelledAgentShowsItsReason(t *testing.T) {
	now := time.Unix(20000, 0)
	m := specialistSidebarModel(t, now)
	m.specialists.complete("plantask_1", specialistCancelled, 0, "cancelled: the run was stopped while this task was running", now)
	m.expandedAgent = "plantask_1"

	rendered := strings.Join(m.sidebarAgentExpansion(m.sidebarSpecialists()[0], 30), "\n")
	if !strings.Contains(plainRender(t, rendered), "cancelled") {
		t.Errorf("a cancelled task must say why:\n%s", rendered)
	}
	if strings.Contains(rendered, zeroTheme.red.Render("cancelled")) {
		t.Error("a cancelled task is not an error and must not be red")
	}
}

// A FIGURE THAT DOES NOT FIT IS DROPPED, NOT TRUNCATED. Prose ending in an
// ellipsis still reads as prose; a number ending in one reads as a different
// number, and this line exists to report a spend.
func TestSpendSegmentsDropRatherThanTruncate(t *testing.T) {
	segments := []string{"1m10s", "3.4K tok", "12 tools"}
	for name, tc := range map[string]struct {
		width int
		want  string
	}{
		"everything fits":       {40, "1m10s · 3.4K tok · 12 tools"},
		"the last one does not": {23, "1m10s · 3.4K tok"},
		"only the first does":   {10, "1m10s"},
		"not even the first":    {3, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := fitSegments(segments, tc.width)
			if got != tc.want {
				t.Errorf("fitSegments(%d) = %q, want %q", tc.width, got, tc.want)
			}
			if lipgloss.Width(got) > tc.width {
				t.Errorf("%q overflows %d cells", got, tc.width)
			}
			if strings.Contains(got, "…") {
				t.Errorf("%q truncated a figure instead of dropping it", got)
			}
		})
	}
}

// "" is the zero value on BOTH sides of the expansion test: expandedAgent when
// nothing is open, and childSessionID for a specialist keyed by a tool-call id
// the provider left blank. Compared bare, such a row is permanently expanded —
// and because the sidebar clips to its height, the uninvited lines push PLAN,
// FILES and ACTIVITY off the bottom of the column.
func TestAnAgentWithNoCardKeyNeverExpandsItself(t *testing.T) {
	now := time.Unix(20000, 0)
	m := sidebarTestModel()
	m.now = func() time.Time { return now }
	m.specialists.start("mystery", "a brief nobody asked to see", "", now.Add(-30*time.Second))
	if m.expandedAgent != "" {
		t.Fatal("sanity check failed: nothing should be expanded")
	}

	width := sidebarWidth(m.width)
	rendered := plainRender(t, strings.Join(m.sidebarAgentLines(width), "\n"))
	if strings.Contains(rendered, "nobody asked to see") {
		t.Errorf("a row with no card key expanded itself against the zero value:\n%s", rendered)
	}
	if hits := m.sidebarAgentSelectables(width); len(hits) != 0 {
		t.Errorf("a row with no card key is not clickable, so it has no way to be opened: %+v", hits)
	}

	// And the sections below it keep their place.
	lines := m.renderContextSidebar(width, m.height)
	var planHeader int
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(plainRender(t, line)), "PLAN") {
			planHeader = i
			break
		}
	}
	if planHeader == 0 || planHeader > 4 {
		t.Errorf("PLAN should sit just below a single-row AGENTS section, found it at line %d", planHeader)
	}
}

// doneAgentsModel: two finished specialists past their linger, one still
// running — the state a plan is in for most of its life.
func doneAgentsModel(t *testing.T, now time.Time) model {
	t.Helper()
	m := sidebarTestModel()
	m.now = func() time.Time { return now }
	for i, name := range []string{"a-reltime", "a-fsutil"} {
		key := fmt.Sprintf("plantask_%d", i+1)
		m.specialists.start(name, "You are auditing package "+name, key, now.Add(-40*time.Second))
		m.specialists.complete(key, specialistCompleted, 0, "", now.Add(-30*time.Second))
		m.specialists.setResult(key, "pkg: 3 findings.\nreltime.go:41 — Parse ignores the tz suffix")
	}
	m.specialists.start("d-report", "producing the report", "plantask_3", now.Add(-9*time.Second))
	return m
}

// A plan's finished tasks ARE its result. They used to vanish a second and a
// half after each one landed, so a nine-task run that succeeded showed an empty
// AGENTS section and no way to ask what any of them did.
func TestTheDoneToggleRevealsFinishedAgents(t *testing.T) {
	now := time.Unix(50000, 0)
	m := doneAgentsModel(t, now)
	width := sidebarWidth(m.width)

	if got := m.doneAgentCount(); got != 2 {
		t.Fatalf("doneAgentCount = %d, want 2", got)
	}
	collapsed := plainRender(t, strings.Join(m.sidebarAgentLines(width), "\n"))
	if strings.Contains(collapsed, "a-reltime") {
		t.Errorf("finished agents stay hidden until asked for:\n%s", collapsed)
	}
	if header := plainRender(t, m.sidebarAgentHeader(width)); !strings.Contains(header, "2 done") {
		t.Errorf("the header must advertise what the toggle would reveal: %q", header)
	}

	// The toggle is clickable at the header row.
	hits := m.sidebarAgentSelectables(width)
	var toggle *sidebarAgentHit
	for i := range hits {
		if hits[i].toggleDone {
			toggle = &hits[i]
		}
	}
	if toggle == nil {
		t.Fatal("the header's toggle must be clickable")
	}
	if toggle.lineOffset != 0 {
		t.Fatalf("the toggle sits on the AGENTS header at offset 0, got %d", toggle.lineOffset)
	}

	x0 := m.chatColumnWidth() + 3
	opened, _, handled := m.handleTranscriptSelectionMouse(
		tea.MouseClickMsg{Button: tea.MouseLeft, X: x0, Y: 0})
	if !handled || !opened.showDoneAgents {
		t.Fatalf("the click must open the finished list: handled=%v shown=%v", handled, opened.showDoneAgents)
	}
	shown := plainRender(t, strings.Join(opened.sidebarAgentLines(width), "\n"))
	for _, want := range []string{"a-reltime", "a-fsutil", "d-report"} {
		if !strings.Contains(shown, want) {
			t.Errorf("expected %q in the opened list:\n%s", want, shown)
		}
	}

	// And it closes again.
	closed, _, _ := opened.handleTranscriptSelectionMouse(
		tea.MouseClickMsg{Button: tea.MouseLeft, X: x0, Y: 0})
	if closed.showDoneAgents {
		t.Error("a second click must close the finished list")
	}
}

// THE STATE THE TOGGLE EXISTS FOR. When every agent has finished there are no
// rows left, so a control hung off the rows would be unclickable in precisely
// the case it is needed.
func TestTheDoneToggleIsClickableWithNoLiveAgents(t *testing.T) {
	now := time.Unix(50000, 0)
	m := doneAgentsModel(t, now)
	m.specialists.complete("plantask_3", specialistCompleted, 0, "", now.Add(-30*time.Second))
	width := sidebarWidth(m.width)

	if len(m.sidebarAgentLines(width)) != 0 {
		t.Fatal("sanity check failed: every agent has finished, so no rows remain")
	}
	hits := m.sidebarAgentSelectables(width)
	if len(hits) != 1 || !hits[0].toggleDone {
		t.Fatalf("the toggle must survive an empty list, got %+v", hits)
	}
	x0 := m.chatColumnWidth() + 3
	opened, _, handled := m.handleTranscriptSelectionMouse(
		tea.MouseClickMsg{Button: tea.MouseLeft, X: x0, Y: 0})
	if !handled || !opened.showDoneAgents {
		t.Fatal("clicking the toggle with no live agents must still open the list")
	}
	if got := len(opened.sidebarAgentLines(width)); got != 3 {
		t.Errorf("expected all three finished agents, got %d rows", got)
	}
}

// WHAT IT PRODUCED, which is the thing the agent was run for. Every other line
// of the expansion says how the work went; this is the work.
func TestAFinishedAgentExpandsToShowWhatItProduced(t *testing.T) {
	now := time.Unix(50000, 0)
	m := doneAgentsModel(t, now)
	m.showDoneAgents = true
	width := sidebarWidth(m.width)

	hits := m.sidebarAgentSelectables(width)
	var row *sidebarAgentHit
	for i := range hits {
		if hits[i].sessionID == "plantask_1" {
			row = &hits[i]
		}
	}
	if row == nil {
		t.Fatalf("the finished agent's row must be clickable, got %+v", hits)
	}

	x0 := m.chatColumnWidth() + 3
	opened, _, handled := m.handleTranscriptSelectionMouse(
		tea.MouseClickMsg{Button: tea.MouseLeft, X: x0, Y: row.lineOffset})
	if !handled || opened.expandedAgent != "plantask_1" {
		t.Fatalf("clicking a finished agent must expand it, got %q", opened.expandedAgent)
	}
	shown := plainRender(t, strings.Join(opened.sidebarAgentLines(width), "\n"))
	// Checked against what a 30-cell column actually renders: the result wraps
	// nothing and truncates per line, so assert on the head of each.
	for _, want := range []string{"3 findings", "reltime.go:41"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the expansion must show what the agent produced (missing %q):\n%s", want, shown)
		}
	}
}
