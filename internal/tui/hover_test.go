package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
)

func TestMouseHoverPredicate(t *testing.T) {
	if !mouseHover(testMouseMotion(tea.MouseNone, 5, 5)) {
		t.Error("a motion event with no button pressed must be a hover")
	}
	if mouseHover(testMouseMotion(tea.MouseLeft, 5, 5)) {
		t.Error("a motion event WITH a button pressed is a drag, not a hover")
	}
	if mouseHover(testMouseClick(tea.MouseLeft, 5, 5)) {
		t.Error("a click is not a hover")
	}
	if mouseHover(testMouseWheel(tea.MouseWheelDown, 5, 5)) {
		t.Error("a wheel event is not a hover")
	}
}

func TestUpdateHoverTargetOnSpecialistCard(t *testing.T) {
	m := specialistClickTestModel(t, "child-sess-1")
	x, y, ok := specialistCardClickPoint(m, 2)
	if !ok {
		t.Fatal("expected a specialist card selectable line in the layout")
	}

	updated, _ := m.Update(testMouseMotion(tea.MouseNone, x, y))
	m = updated.(model)
	if m.hover.kind != hoverTranscript {
		t.Fatalf("hover.kind = %v, want hoverTranscript over a specialist card", m.hover.kind)
	}
}

func TestUpdateHoverTargetOnToggleRow(t *testing.T) {
	m := mouseTestModel()
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowReasoning, text: "private thought"})

	width := m.chatColumnWidth()
	body, selectable := m.transcriptBody(width, "")
	start, _, top := m.transcriptViewportStart(body, width)
	var target transcriptSelectableLine
	for _, line := range selectable {
		if line.toggle {
			target = line
			break
		}
	}
	if !target.toggle {
		t.Fatalf("expected reasoning header to be clickable, selectable=%#v", selectable)
	}

	updated, _ := m.Update(testMouseMotion(tea.MouseNone, target.textStart, top+target.bodyY-start))
	m = updated.(model)
	if m.hover.kind != hoverTranscript {
		t.Fatalf("hover.kind = %v, want hoverTranscript over a toggle header", m.hover.kind)
	}
	if m.hover.bodyY != target.bodyY {
		t.Fatalf("hover.bodyY = %d, want %d", m.hover.bodyY, target.bodyY)
	}
}

func TestUpdateHoverTargetOnPlainTextIsNone(t *testing.T) {
	m := mouseTestModel()
	m.transcript = appendRow(m.transcript, rowUser, "hello world")
	textY := firstTranscriptTextMouseY(t, m)

	updated, _ := m.Update(testMouseMotion(tea.MouseNone, 3, textY))
	m = updated.(model)
	if m.hover.kind != hoverNone {
		t.Fatalf("hover.kind = %v, want hoverNone over plain (non-clickable) text", m.hover.kind)
	}
}

func TestUpdateHoverTargetOnSidebarAgentRow(t *testing.T) {
	// Only a SWARM member row (a session mapped via swarmSessionMap) is clickable
	// in the sidebar — a Task-delegation specialist row is not (sidebarAgentRows
	// only records hits from the swarm loop). swarmSidebarTestModel builds exactly
	// that: real conversation + a mapped swarm member session.
	m := swarmSidebarTestModel(t, map[string]string{"subagent-1": "sess-1"})
	if !m.sidebarActive() {
		t.Fatal("sanity check failed: sidebar should be active with a swarm member present on a 100-col terminal")
	}
	hits := m.sidebarAgentSelectables(sidebarWidth(m.width))
	if len(hits) == 0 {
		t.Fatal("expected at least one clickable sidebar agent row")
	}
	x0 := m.chatColumnWidth() + 3

	updated, _ := m.Update(testMouseMotion(tea.MouseNone, x0+1, hits[0].lineOffset))
	m = updated.(model)
	if m.hover.kind != hoverSidebarAgent {
		t.Fatalf("hover.kind = %v, want hoverSidebarAgent", m.hover.kind)
	}
	if m.hover.sessionID != hits[0].sessionID {
		t.Fatalf("hover.sessionID = %q, want %q", m.hover.sessionID, hits[0].sessionID)
	}
	// The bug this guards: hoverTarget must store the STABLE identity (sessionID),
	// not a raw line offset — resolve it back to a line offset the same way the
	// render path does (hoveredSidebarLineOffset) and confirm it lands on the
	// SAME row that was actually hovered, not some other row at that raw index.
	resolved, ok := m.hoveredSidebarLineOffset(sidebarWidth(m.width))
	if !ok || resolved != hits[0].lineOffset {
		t.Fatalf("hoveredSidebarLineOffset() = (%d, %v), want (%d, true)", resolved, ok, hits[0].lineOffset)
	}
}

func TestUpdateHoverTargetOnPlanStep(t *testing.T) {
	m := runningPlanModel(t, 3)
	m.altScreen = true
	m.height = 40
	// sidebarActive requires real conversation; runningPlanModel only sets m.plan,
	// leaving the transcript at its default (welcome-only, i.e. "empty").
	m.transcript = appendRow(m.transcript, rowUser, "do something")
	if !m.sidebarActive() {
		t.Fatal("sanity check failed: sidebar should be active with a plan present on a 100-col terminal")
	}
	hits := m.sidebarPlanSelectables(sidebarWidth(m.width))
	if len(hits) == 0 {
		t.Fatal("expected at least one clickable plan step row")
	}
	x0 := m.chatColumnWidth() + 3

	updated, _ := m.Update(testMouseMotion(tea.MouseNone, x0+1, hits[0].lineOffset))
	m = updated.(model)
	if m.hover.kind != hoverPlanStep {
		t.Fatalf("hover.kind = %v, want hoverPlanStep", m.hover.kind)
	}
	if m.hover.stepIndex != hits[0].stepIndex {
		t.Fatalf("hover.stepIndex = %d, want %d", m.hover.stepIndex, hits[0].stepIndex)
	}
	// The bug this guards: the render path must resolve stepIndex back to the
	// CORRECT current lineOffset (hits[0].lineOffset, which is >= base=4, never a
	// raw small index like 0/1/2) — not treat stepIndex itself as a line offset
	// into the rendered sidebar (which would land on the AGENTS header/body).
	resolved, ok := m.hoveredSidebarLineOffset(sidebarWidth(m.width))
	if !ok || resolved != hits[0].lineOffset {
		t.Fatalf("hoveredSidebarLineOffset() = (%d, %v), want (%d, true)", resolved, ok, hits[0].lineOffset)
	}
}

func TestUpdateHoverTargetOnPermissionOptionMovesCursor(t *testing.T) {
	m := mouseTestModel()
	m.pending = true // the permission prompt only renders into the transcript body while m.pending
	m.pendingPermission = &pendingPermissionPrompt{
		request: agent.PermissionRequest{ToolName: "write_file"},
		cursor:  0,
	}
	width := m.chatColumnWidth()
	body, selectable := m.transcriptBody(width, "")
	start, _, top := m.transcriptViewportStart(body, width)
	var target transcriptSelectableLine
	found := false
	for _, line := range selectable {
		if line.permOption && line.permChoice != permissionOptions(m.pendingPermission.request)[0].choice {
			target = line
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected at least 2 permission options to test a non-default hover target")
	}

	updated, _ := m.Update(testMouseMotion(tea.MouseNone, target.textStart, top+target.bodyY-start))
	m = updated.(model)
	// Permission-option hover reuses the existing cursor highlight, not m.hover.
	if m.hover.kind != hoverNone {
		t.Fatalf("hover.kind = %v, want hoverNone (permission options use pendingPermission.cursor)", m.hover.kind)
	}
	wantIndex := -1
	for index, option := range permissionOptions(m.pendingPermission.request) {
		if option.choice == target.permChoice {
			wantIndex = index
			break
		}
	}
	if m.pendingPermission.cursor != wantIndex {
		t.Fatalf("pendingPermission.cursor = %d, want %d (the hovered option)", m.pendingPermission.cursor, wantIndex)
	}
}

func TestHoverChangesTranscriptRenderOutput(t *testing.T) {
	m := specialistClickTestModel(t, "child-sess-1")
	width := m.chatColumnWidth()
	without := m.transcriptBodyLayout(width, "").String()

	x, y, ok := specialistCardClickPoint(m, 2)
	if !ok {
		t.Fatal("expected a specialist card selectable line in the layout")
	}
	updated, _ := m.Update(testMouseMotion(tea.MouseNone, x, y))
	m = updated.(model)
	if m.hover.kind != hoverTranscript {
		t.Fatal("sanity check failed: hover should be set over the specialist card")
	}
	with := m.transcriptBodyLayout(width, "").String()

	if without == with {
		t.Fatal("rendering with a hover target set should differ from without (the highlight should show)")
	}
}

func TestHoverChangesSidebarRenderOutput(t *testing.T) {
	m := swarmSidebarTestModel(t, map[string]string{"subagent-1": "sess-1"})
	width := m.chatColumnWidth()
	height := m.height
	without := strings.Join(m.renderContextSidebar(sidebarWidth(m.width), height), "\n")

	hits := m.sidebarAgentSelectables(sidebarWidth(m.width))
	if len(hits) == 0 {
		t.Fatal("expected at least one clickable sidebar agent row")
	}
	updated, _ := m.Update(testMouseMotion(tea.MouseNone, width+3+1, hits[0].lineOffset))
	m = updated.(model)
	if m.hover.kind != hoverSidebarAgent {
		t.Fatal("sanity check failed: hover should be set over the sidebar agent row")
	}
	with := strings.Join(m.renderContextSidebar(sidebarWidth(m.width), height), "\n")

	if without == with {
		t.Fatal("sidebar rendering with a hover target set should differ from without (the highlight should show)")
	}
}

func TestHoverClearsOnWheelScroll(t *testing.T) {
	m := mouseTestModel()
	for range 80 {
		m.transcript = appendRow(m.transcript, rowUser, "line content")
	}
	m.hover = hoverTarget{kind: hoverTranscript, bodyY: 5}

	updated, _ := m.Update(testMouseWheel(tea.MouseWheelUp, 0, 3))
	m = updated.(model)
	if m.hover.kind != hoverNone {
		t.Fatalf("hover should clear on a wheel scroll, got %#v", m.hover)
	}
}

func TestHoverClearsOnSubchatExit(t *testing.T) {
	m := mouseTestModel()
	m.subchat.active = true
	m.subchat.childSessionID = "child-1"
	m.subchat.childRows = appendRow(nil, rowUser, "child text")
	m.hover = hoverTarget{kind: hoverTranscript, bodyY: 5}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = updated.(model)
	if m.subchat.active {
		t.Fatal("Esc should exit the subchat view")
	}
	if m.hover.kind != hoverNone {
		t.Fatalf("hover should clear on subchat exit (bodyY numbering differs), got %#v", m.hover)
	}
}

// A sidebar row can disappear between when it was hovered and the next render
// (a swarm member's linger window elapsing) with no mouse motion in between to
// re-target the hover. hoveredSidebarLineOffset must not paint a highlight on
// whatever unrelated row now occupies that identity's old slot — it must find
// nothing and skip painting entirely.
func TestHoveredSidebarLineOffsetSelfHealsWhenRowDisappears(t *testing.T) {
	m := swarmSidebarTestModel(t, map[string]string{"subagent-1": "sess-1"})
	hits := m.sidebarAgentSelectables(sidebarWidth(m.width))
	if len(hits) == 0 {
		t.Fatal("expected at least one clickable sidebar agent row")
	}
	m.hover = hoverTarget{kind: hoverSidebarAgent, sessionID: hits[0].sessionID}

	// Simulate the member disappearing (a fresh model with no swarm member at all,
	// same identity no longer present) without any mouse motion clearing m.hover.
	m2 := swarmSidebarTestModel(t, nil)
	m2.hover = m.hover

	if _, ok := m2.hoveredSidebarLineOffset(sidebarWidth(m2.width)); ok {
		t.Fatal("hoveredSidebarLineOffset should find nothing once the identity no longer resolves, not fall back to a stale/coincidental row")
	}
}
