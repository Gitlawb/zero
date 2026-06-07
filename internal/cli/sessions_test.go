package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/sessions"
)

func TestRunSessionsListsLineageAndTree(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: sequenceClockCLI([]time.Time{
		time.Date(2026, 6, 4, 19, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 4, 19, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 4, 19, 0, 2, 0, time.UTC),
		time.Date(2026, 6, 4, 19, 0, 3, 0, time.UTC),
		time.Date(2026, 6, 4, 19, 0, 4, 0, time.UTC),
	})})
	root, err := store.Create(sessions.CreateInput{SessionID: "root", Title: "Root session"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	child, err := store.CreateChild(root.SessionID, sessions.ChildInput{
		SessionID: "child",
		Title:     "Review child",
		Tag:       "specialist",
		Depth:     1,
		AgentName: "code-review",
		TaskID:    "task-7",
	})
	if err != nil {
		t.Fatalf("CreateChild returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sessions", "list"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("sessions list exit = %d, stderr = %q", exitCode, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Zero sessions") || !strings.Contains(output, root.SessionID) || !strings.Contains(output, child.SessionID) || !strings.Contains(output, "code-review") || !strings.Contains(output, "tag=specialist") || !strings.Contains(output, "depth=1") {
		t.Fatalf("sessions list output = %q, want root, child, and agent", output)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sessions", "lineage", child.SessionID}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("sessions lineage exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "root -> child") {
		t.Fatalf("sessions lineage output = %q, want root-to-child path", got)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sessions", "tree", root.SessionID, "--json"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("sessions tree exit = %d, stderr = %q", exitCode, stderr.String())
	}
	var tree sessions.TreeNode
	if err := json.Unmarshal(stdout.Bytes(), &tree); err != nil {
		t.Fatalf("sessions tree JSON did not decode: %v\n%s", err, stdout.String())
	}
	if tree.Session.SessionID != root.SessionID || len(tree.Children) != 1 || tree.Children[0].Session.SessionID != child.SessionID {
		t.Fatalf("sessions tree JSON = %#v, want root with one child", tree)
	}
}

func TestRunSessionsPlansRewindAndCompaction(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedCLITime("2026-06-04T19:30:00Z")})
	session, err := store.Create(sessions.CreateInput{SessionID: "plan", Title: "Plan session"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	for _, content := range []string{"alpha", "beta", "gamma", "delta"} {
		if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatal(err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sessions", "rewind-plan", session.SessionID, "--sequence", "2", "--json"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("sessions rewind-plan exit = %d, stderr = %q", exitCode, stderr.String())
	}
	var rewind sessions.RewindPlan
	if err := json.Unmarshal(stdout.Bytes(), &rewind); err != nil {
		t.Fatalf("rewind-plan JSON did not decode: %v\n%s", err, stdout.String())
	}
	if rewind.TargetEventID != "plan:2" || rewind.KeptCount != 2 || rewind.DroppedCount != 2 {
		t.Fatalf("unexpected rewind plan: %#v", rewind)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sessions", "compact-plan", session.SessionID, "--preserve-last", "1", "--max-prompt-chars", "500", "--json"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("sessions compact-plan exit = %d, stderr = %q", exitCode, stderr.String())
	}
	var compact sessions.CompactionPlan
	if err := json.Unmarshal(stdout.Bytes(), &compact); err != nil {
		t.Fatalf("compact-plan JSON did not decode: %v\n%s", err, stdout.String())
	}
	if compact.CompactableCount != 3 || compact.PreservedCount != 1 || !strings.Contains(compact.SummaryPrompt, "alpha") {
		t.Fatalf("unexpected compaction plan: %#v", compact)
	}
}

func TestRunSessionsValidatesArgs(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedCLITime("2026-06-04T19:30:00Z")})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sessions", "children", "missing"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitUsage {
		t.Fatalf("sessions children exit = %d, want usage", exitCode)
	}
	if !strings.Contains(stderr.String(), "Zero session not found: missing") {
		t.Fatalf("sessions children stderr = %q, want missing-session error", stderr.String())
	}

	for _, test := range []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "unknown command", args: []string{"sessions", "foo"}, wantStderr: `unknown sessions command "foo"`},
		{name: "list extra arg", args: []string{"sessions", "list", "extra"}, wantStderr: "sessions list does not accept positional arguments"},
		{name: "rewind flag on list", args: []string{"sessions", "list", "--sequence", "2"}, wantStderr: "only valid for sessions rewind-plan"},
		{name: "compaction flag on tree", args: []string{"sessions", "tree", "root", "--preserve-last", "2"}, wantStderr: "only valid for sessions compact-plan"},
		{name: "empty event flag", args: []string{"sessions", "list", "--event="}, wantStderr: "--event requires a value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			exitCode := runWithDeps(test.args, &stdout, &stderr, appDeps{
				newSessionStore: func() *sessions.Store {
					return store
				},
			})
			if exitCode != exitUsage {
				t.Fatalf("%v exit = %d, want usage", test.args, exitCode)
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("%v stderr = %q, want %q", test.args, stderr.String(), test.wantStderr)
			}
		})
	}
}

func sequenceClockCLI(values []time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}
