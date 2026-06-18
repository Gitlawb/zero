package swarm

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/specialist"
	"github.com/Gitlawb/zero/internal/streamjson"
)

// TestSpecialistLauncherRunsUnregisteredSwarmAgent guards the fix for the swarm
// catch-22: swarm_spawn only accepts agent types "subagent"/"teammate", but the
// launcher previously looked those up as specialist NAMES (registry has only
// worker/explorer/code-review), so every member failed with "specialist ... not
// found". The launcher now runs the member from an inline manifest built from its
// swarm definition, so an unregistered agent type executes end-to-end.
func TestSpecialistLauncherRunsUnregisteredSwarmAgent(t *testing.T) {
	zero := 0
	var ran bool
	var gotArgs []string
	executor := specialist.Executor{
		BinaryPath:   "/usr/local/bin/zero",
		NewSessionID: func() (string, error) { return "member_task", nil },
		// No "subagent" specialist is registered — the old name-lookup path failed.
		Load: func(specialist.LoadOptions) (specialist.LoadResult, error) {
			return specialist.LoadResult{}, nil
		},
		RunChild: func(ctx context.Context, binaryPath string, args []string) (specialist.ChildRunResult, error) {
			ran = true
			gotArgs = append([]string(nil), args...)
			return specialist.ChildRunResult{Events: []streamjson.Event{
				{Type: streamjson.EventRunStart, SessionID: "member_task"},
				{Type: streamjson.EventFinal, Text: "member done"},
				{Type: streamjson.EventRunEnd, Status: "success", ExitCode: &zero},
			}}, nil
		},
	}

	handle, err := NewSpecialistLauncher(executor).Launch(context.Background(), MemberSpec{
		ID:           "m1",
		TaskID:       "m1",
		AgentType:    "subagent",
		Team:         "probe",
		Task:         "count files",
		SystemPrompt: "You are a subagent spawned to complete a specific task.\n\nTask: count files",
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	res, err := handle.Wait()
	if err != nil {
		t.Fatalf("member run failed: %v", err)
	}
	if !ran {
		t.Fatal("member never executed (RunChild not called) — the catch-22 is back")
	}
	if !strings.Contains(res.Result, "member done") {
		t.Fatalf("unexpected member result: %q", res.Result)
	}
	if res.SessionID != "member_task" {
		t.Fatalf("session id = %q", res.SessionID)
	}
	// The unregistered swarm agent type titled the child session (the inline
	// manifest, not a registry lookup, drove the run).
	if !strings.Contains(strings.Join(gotArgs, " "), "subagent") {
		t.Fatalf("member args missing agent type: %#v", gotArgs)
	}
}
