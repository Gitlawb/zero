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
	if !strings.Contains(output, "Zero sessions") || !strings.Contains(output, root.SessionID) || !strings.Contains(output, child.SessionID) || !strings.Contains(output, "code-review") {
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
