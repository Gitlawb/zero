package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// buildLargeSearchTree creates n files, each containing a grep-matchable
// line, so a walk that does NOT respect cancellation would have plenty of
// work left to do (and matches to find) after the very first entry.
func buildLargeSearchTree(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		path := filepath.Join(root, "file"+strconv.Itoa(i)+".txt")
		if err := os.WriteFile(path, []byte("needle\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return root
}

// A cancelled run must stop the filesystem walk promptly instead of visiting
// every remaining entry to completion. Before this fix, grep's Run/
// RunWithSandbox discarded their context entirely, so cancelling the run
// (Ctrl+C / /exit) had no effect on an in-flight, unscoped search: the walk
// ran to completion regardless, and — because the TUI's exit path waits for
// the cancelled run's own response before it can quit — the whole
// application was stuck until the walk finished on its own.
func TestGrepStopsWalkOnCancelledContext(t *testing.T) {
	root := buildLargeSearchTree(t, 500)
	tool := NewGrepTool(root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the walk starts

	result := tool.Run(ctx, map[string]any{"pattern": "needle"})
	if result.Status != StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if result.Output != "Error: grep cancelled." {
		t.Fatalf("output = %q, want the cancellation message", result.Output)
	}
}

func TestGrepRunWithSandboxStopsWalkOnCancelledContext(t *testing.T) {
	root := buildLargeSearchTree(t, 500)
	tool := NewGrepTool(root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sandboxed := tool.(sandboxAwareTool)
	result := sandboxed.RunWithSandbox(ctx, map[string]any{"pattern": "needle"}, nil)
	if result.Status != StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if result.Output != "Error: grep cancelled." {
		t.Fatalf("output = %q, want the cancellation message", result.Output)
	}
}

// grep with a live (non-cancelled) context must still work normally: this
// fix only short-circuits on cancellation, it does not change matching.
func TestGrepStillMatchesWithLiveContext(t *testing.T) {
	root := buildLargeSearchTree(t, 3)
	tool := NewGrepTool(root)

	result := tool.Run(context.Background(), map[string]any{"pattern": "needle"})
	if result.Status != StatusOK {
		t.Fatalf("status = %q, want ok; output=%q", result.Status, result.Output)
	}
}

func TestGlobStopsWalkOnCancelledContext(t *testing.T) {
	root := buildLargeSearchTree(t, 500)
	tool := NewGlobTool(root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := tool.Run(ctx, map[string]any{"pattern": "**/*.txt"})
	if result.Status != StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if result.Output != "Error: glob cancelled." {
		t.Fatalf("output = %q, want the cancellation message", result.Output)
	}
}

func TestGlobRunWithSandboxStopsWalkOnCancelledContext(t *testing.T) {
	root := buildLargeSearchTree(t, 500)
	tool := NewGlobTool(root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sandboxed := tool.(sandboxAwareTool)
	result := sandboxed.RunWithSandbox(ctx, map[string]any{"pattern": "**/*.txt"}, nil)
	if result.Status != StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if result.Output != "Error: glob cancelled." {
		t.Fatalf("output = %q, want the cancellation message", result.Output)
	}
}

func TestGlobStillMatchesWithLiveContext(t *testing.T) {
	root := buildLargeSearchTree(t, 3)
	tool := NewGlobTool(root)

	result := tool.Run(context.Background(), map[string]any{"pattern": "**/*.txt"})
	if result.Status != StatusOK {
		t.Fatalf("status = %q, want ok; output=%q", result.Status, result.Output)
	}
}
