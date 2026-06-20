package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

type fakeExecSessionTool struct {
	sessions []tools.ExecSessionSnapshot
	stopped  []int
	stopAll  bool
}

func (tool *fakeExecSessionTool) Name() string { return tools.ExecCommandToolName }

func (tool *fakeExecSessionTool) Description() string { return "fake exec sessions" }

func (tool *fakeExecSessionTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}

func (tool *fakeExecSessionTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectShell, Permission: tools.PermissionPrompt}
}

func (tool *fakeExecSessionTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}

func (tool *fakeExecSessionTool) ExecSessions() []tools.ExecSessionSnapshot {
	return append([]tools.ExecSessionSnapshot(nil), tool.sessions...)
}

func (tool *fakeExecSessionTool) StopExecSession(id int) bool {
	for _, session := range tool.sessions {
		if session.ID == id {
			tool.stopped = append(tool.stopped, id)
			return true
		}
	}
	return false
}

func (tool *fakeExecSessionTool) StopAllExecSessions() []int {
	tool.stopAll = true
	ids := make([]int, 0, len(tool.sessions))
	for _, session := range tool.sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

func modelWithFakeExecSessions(tool *fakeExecSessionTool, now time.Time) model {
	registry := tools.NewRegistry()
	registry.Register(tool)
	return model{
		registry: registry,
		now:      func() time.Time { return now },
	}
}

func TestBackgroundTerminalsTextEmpty(t *testing.T) {
	m := modelWithFakeExecSessions(&fakeExecSessionTool{}, time.Unix(100, 0))

	text := m.backgroundTerminalsText()
	for _, want := range []string{"Background Terminals", "0 running", "No background terminals running."} {
		if !strings.Contains(text, want) {
			t.Fatalf("backgroundTerminalsText missing %q:\n%s", want, text)
		}
	}
}

func TestBackgroundTerminalsTextListsSessions(t *testing.T) {
	now := time.Unix(200, 0)
	m := modelWithFakeExecSessions(&fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{{
			ID:           1000,
			Command:      "python3 -m http.server 8000",
			RelativeCwd:  ".",
			StartedAt:    now.Add(-2 * time.Minute),
			Status:       "running",
			RecentOutput: "Serving HTTP on 0.0.0.0 port 8000",
		}},
	}, now)

	text := m.backgroundTerminalsText()
	for _, want := range []string{"Background Terminals", "1000", "running", "2m", "python3 -m http.server 8000", "/stop <session_id>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("backgroundTerminalsText missing %q:\n%s", want, text)
		}
	}
	if summary := m.backgroundTerminalSummary(); summary != "1 background terminal running · /ps to view · /stop to close" {
		t.Fatalf("summary = %q", summary)
	}
}

func TestStopBackgroundTerminalsTextStopsAllAndOne(t *testing.T) {
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{
			{ID: 1000, StartedAt: time.Unix(100, 0), Status: "running"},
			{ID: 1001, StartedAt: time.Unix(100, 0), Status: "running"},
		},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))

	all := m.stopBackgroundTerminalsText("")
	if !tool.stopAll || !strings.Contains(all, "stopping 1000, 1001") {
		t.Fatalf("stop all did not render expected result: stopAll=%v text=%s", tool.stopAll, all)
	}

	one := m.stopBackgroundTerminalsText("1001")
	if len(tool.stopped) != 1 || tool.stopped[0] != 1001 || !strings.Contains(one, "stopping 1001") {
		t.Fatalf("stop one did not render expected result: stopped=%#v text=%s", tool.stopped, one)
	}
}

func TestStopBackgroundTerminalsTextRejectsInvalidSessionID(t *testing.T) {
	m := modelWithFakeExecSessions(&fakeExecSessionTool{}, time.Unix(100, 0))

	text := m.stopBackgroundTerminalsText("abc")
	if !strings.Contains(text, "Usage: /stop [session_id]") {
		t.Fatalf("expected usage, got:\n%s", text)
	}
}

func TestQuitStopsBackgroundTerminals(t *testing.T) {
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{{ID: 1000, StartedAt: time.Unix(100, 0), Status: "running"}},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))

	_, _ = m.quit()
	if !tool.stopAll {
		t.Fatal("quit should stop all background terminal sessions")
	}
}
