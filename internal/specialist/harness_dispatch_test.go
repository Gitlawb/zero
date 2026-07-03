package specialist

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/streamjson"
)

// stubHarnessOnPath creates an empty (never executed — RunHarnessChild
// intercepts before any real exec) executable file named binary under a fresh
// temp dir and points PATH at it, so agentcli.DetectOne(binary, ...) resolves
// successfully without needing the real CLI installed.
func stubHarnessOnPath(t *testing.T, binary string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, binary)
	if err := os.WriteFile(path, []byte(""), 0o755); err != nil {
		t.Fatalf("write stub binary: %v", err)
	}
	t.Setenv("PATH", dir)
}

// harnessManifest builds a minimal valid manifest pinned to the given
// agentcli harness id, for exercising runFresh's harness-dispatch branch
// without going through the markdown loader.
func harnessManifest(t *testing.T, harness string) *Manifest {
	t.Helper()
	manifest, err := ParseMarkdown(`---
name: harness-worker
description: Runs on an external harness
harness: ` + harness + `
---
Do the task.`)
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	return &manifest
}

// TestRunFreshHarnessRejectsRunInBackground locks in that a harness-backed
// specialist (Metadata.Harness set) refuses RunInBackground before ever
// touching runHarness/agentcli.DetectOne — the background-launch path assumes
// a self-exec zero child it can register with background.Manager, which a
// foreign harness CLI is not.
func TestRunFreshHarnessRejectsRunInBackground(t *testing.T) {
	executor := Executor{}
	_, err := executor.Run(context.Background(), TaskParameters{
		Prompt:          "do the thing",
		RunInBackground: true,
		Manifest:        harnessManifest(t, "codex"),
	}, TaskRunOptions{})
	if err == nil {
		t.Fatal("expected an error for a harness specialist run in background")
	}
	if !strings.Contains(err.Error(), "cannot run in background") {
		t.Fatalf("error = %q, want mention of \"cannot run in background\"", err.Error())
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Fatalf("error = %q, want it to name the harness", err.Error())
	}
}

// TestRunFreshDispatchesHarnessNotInstalled locks in that runFresh routes a
// harness-pinned manifest to runHarness (rather than the self-exec BuildArgs
// path), by observing runHarness's "not installed" error when the harness
// binary cannot be found on PATH. PATH is redirected to an empty directory so
// this is deterministic regardless of what happens to be installed on the
// host running the test.
func TestRunFreshDispatchesHarnessNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	executor := Executor{}
	_, err := executor.Run(context.Background(), TaskParameters{
		Prompt:   "do the thing",
		Manifest: harnessManifest(t, "codex"),
	}, TaskRunOptions{})
	if err == nil {
		t.Fatal("expected an error when the harness binary is not on PATH")
	}
	if !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error = %q, want a \"codex ... not installed\" message", err.Error())
	}
}

// TestRunHarnessSuccessWiresAccountingAndResultViaStubbedChild covers the
// success path runHarness's RunHarnessChild seam exists for: session ID
// resolution, decoder selection (codex-json), and BuildFinalResult shaping —
// end to end, without spawning a real codex process. Before this seam
// existed, exercising this path required an actual installed claude/codex
// binary.
func TestRunHarnessSuccessWiresAccountingAndResultViaStubbedChild(t *testing.T) {
	stubHarnessOnPath(t, "codex")

	var gotBinaryPath string
	var gotArgs []string
	executor := Executor{
		NewSessionID: func() (string, error) { return "harness_child_session", nil },
		RunHarnessChild: func(_ context.Context, binaryPath string, args []string, _ string, decoder childDecoder, _ func(streamjson.Event)) (ChildRunResult, error) {
			gotBinaryPath = binaryPath
			gotArgs = args
			if _, ok := decoder.(*codexJSONDecoder); !ok {
				t.Fatalf("decoder = %T, want *codexJSONDecoder for the codex harness", decoder)
			}
			return ChildRunResult{
				Events: []streamjson.Event{
					{Type: streamjson.EventText, Delta: "hi"},
					{Type: streamjson.EventFinal, Text: "hi"},
				},
				ExitCode: 0,
			}, nil
		},
	}

	result, err := executor.Run(context.Background(), TaskParameters{
		Prompt:   "reply with exactly: hi",
		Manifest: harnessManifest(t, "codex"),
	}, TaskRunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.SessionID != "harness_child_session" {
		t.Fatalf("SessionID = %q, want the injected NewSessionID value", result.SessionID)
	}
	if !strings.HasSuffix(gotBinaryPath, "codex") {
		t.Fatalf("binaryPath = %q, want the stubbed codex path", gotBinaryPath)
	}
	if len(gotArgs) == 0 {
		t.Fatal("expected PrintArgs to have built a non-empty argv for the stubbed child")
	}
	if result.Result.Output != "hi" {
		t.Fatalf("Result.Output = %q, want %q (from BuildFinalResult over the stubbed child's events)", result.Result.Output, "hi")
	}
}
