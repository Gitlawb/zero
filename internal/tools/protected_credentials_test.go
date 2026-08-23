package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEngineLessRegistryMatrix drives every registry-dispatched tool that
// names a path through the plain registry API (Registry.Run — no sandbox
// engine) with a protected token selected, and separately with no token
// selected at all. Before protected_credentials.go this matrix would have
// failed on read_file, read_minified_file, grep, and glob: nothing upstream of
// those tools' own Run() ever consulted the protected set when no engine was
// supplied, because Registry.RunWithOptions only asks the engine at all when
// options.Sandbox is non-nil.
func TestEngineLessRegistryMatrix(t *testing.T) {
	setup := func(t *testing.T) (ws, token string) {
		t.Helper()
		ws = t.TempDir()
		token = filepath.Join(ws, "bridge-token")
		if err := os.WriteFile(token, []byte("bridge-secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "ordinary.txt"), []byte("ordinary\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ZERO_DAEMON_REMOTE_TOKEN", "")
		t.Setenv("ZERO_DAEMON_REMOTE_TOKEN_FILE", token)
		return ws, token
	}

	t.Run("read_file", func(t *testing.T) {
		ws, _ := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedReadFileTool(ws, nil))
		result := registry.Run(context.Background(), "read_file", map[string]any{"path": "bridge-token"})
		if result.Status == StatusOK || strings.Contains(result.Output, "bridge-secret") {
			t.Fatalf("read_file leaked token: %+v", result)
		}
	})

	t.Run("read_minified_file", func(t *testing.T) {
		ws, _ := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedReadMinifiedFileTool(ws, nil))
		result := registry.Run(context.Background(), "read_minified_file", map[string]any{"path": "bridge-token"})
		if result.Status == StatusOK || strings.Contains(result.Output, "bridge-secret") {
			t.Fatalf("read_minified_file leaked token: %+v", result)
		}
	})

	t.Run("grep", func(t *testing.T) {
		ws, _ := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedGrepTool(ws, nil))
		result := registry.Run(context.Background(), "grep", map[string]any{"pattern": "bridge-secret"})
		if strings.Contains(result.Output, "bridge-token") {
			t.Fatalf("grep surfaced the protected filename: %+v", result)
		}
	})

	t.Run("glob", func(t *testing.T) {
		ws, _ := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedGlobTool(ws, nil))
		result := registry.Run(context.Background(), "glob", map[string]any{"pattern": "*"})
		if strings.Contains(result.Output, "bridge-token") {
			t.Fatalf("glob surfaced the protected filename: %+v", result)
		}
	})

	t.Run("list_directory", func(t *testing.T) {
		ws, _ := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedListDirectoryTool(ws, nil))
		result := registry.Run(context.Background(), "list_directory", map[string]any{"path": "."})
		if strings.Contains(result.Output, "bridge-token") {
			t.Fatalf("list_directory surfaced the protected filename: %+v", result)
		}
	})

	t.Run("write_file", func(t *testing.T) {
		ws, token := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedWriteFileTool(ws, nil))
		result := registry.Run(context.Background(), "write_file", map[string]any{"path": "bridge-token", "content": "attacker\n", "overwrite": true})
		if result.Status == StatusOK {
			t.Fatalf("write_file overwrote the protected token: %+v", result)
		}
		contents, err := os.ReadFile(token)
		if err != nil || string(contents) != "bridge-secret\n" {
			t.Fatalf("token changed after a denied write: contents=%q err=%v", contents, err)
		}
	})

	t.Run("edit_file", func(t *testing.T) {
		ws, token := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedEditFileTool(ws, nil))
		result := registry.Run(context.Background(), "edit_file", map[string]any{"path": "bridge-token", "old_string": "bridge-secret", "new_string": "attacker"})
		if result.Status == StatusOK {
			t.Fatalf("edit_file rewrote the protected token: %+v", result)
		}
		contents, err := os.ReadFile(token)
		if err != nil || string(contents) != "bridge-secret\n" {
			t.Fatalf("token changed after a denied edit: contents=%q err=%v", contents, err)
		}
	})

	t.Run("apply_patch unified", func(t *testing.T) {
		ws, token := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedApplyPatchTool(ws, nil))
		patch := "--- a/bridge-token\n+++ b/bridge-token\n@@ -1 +1 @@\n-bridge-secret\n+attacker\n"
		result := registry.Run(context.Background(), "apply_patch", map[string]any{"patch": patch})
		if result.Status == StatusOK {
			t.Fatalf("apply_patch rewrote the protected token: %+v", result)
		}
		contents, err := os.ReadFile(token)
		if err != nil || string(contents) != "bridge-secret\n" {
			t.Fatalf("token changed after a denied unified patch: contents=%q err=%v", contents, err)
		}
	})

	t.Run("apply_patch structured", func(t *testing.T) {
		ws, token := setup(t)
		registry := NewRegistry()
		registry.Register(NewScopedApplyPatchTool(ws, nil))
		patch := "*** Begin Patch\n*** Update File: bridge-token\n@@\n-bridge-secret\n+attacker\n*** End Patch\n"
		result := registry.Run(context.Background(), "apply_patch", map[string]any{"patch": patch})
		if result.Status == StatusOK {
			t.Fatalf("apply_patch (structured) rewrote the protected token: %+v", result)
		}
		contents, err := os.ReadFile(token)
		if err != nil || string(contents) != "bridge-secret\n" {
			t.Fatalf("token changed after a denied structured patch: contents=%q err=%v", contents, err)
		}
	})

	// Ordinary content must still flow through every tool with no token
	// configured at all — the mandatory guard must never turn into a permanent
	// deny of everything.
	t.Run("no token selected", func(t *testing.T) {
		ws := t.TempDir()
		if err := os.WriteFile(filepath.Join(ws, "ordinary.txt"), []byte("ordinary\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ZERO_DAEMON_REMOTE_TOKEN", "")
		t.Setenv("ZERO_DAEMON_REMOTE_TOKEN_FILE", "")

		registry := NewRegistry()
		registry.Register(NewScopedReadFileTool(ws, nil))
		result := registry.Run(context.Background(), "read_file", map[string]any{"path": "ordinary.txt"})
		if result.Status != StatusOK || !strings.Contains(result.Output, "ordinary") {
			t.Fatalf("read_file with no token configured wrongly refused: %+v", result)
		}
	})
}

// TestProtectedReadOpenClosesTheCheckToUseWindow is the deterministic
// swap-race regression: a pathname check followed by a SEPARATE later open
// (what every direct file tool did before protectedReadOpen existed) leaves a
// window where a concurrent writer can replace an already-checked ordinary
// file with a symlink to the token before the later open runs. protectedReadOpen
// closes that window by deciding from the SAME handle content is read through,
// so there is no separate "check" step to race at all — every open
// independently re-verifies identity from its own freshly-obtained os.FileInfo.
//
// This is exercised without real goroutine concurrency (which cannot be made
// deterministic) by proving the invariant that makes the race impossible: an
// ordinary path served successfully once is re-verified, not cached, the next
// time it is opened — so a path that becomes the protected token BETWEEN two
// calls is caught on the second call exactly as if it had always been the
// token. Nothing about the first call's outcome could ever leak into the
// second.
func TestProtectedReadOpenClosesTheCheckToUseWindow(t *testing.T) {
	ws := t.TempDir()
	token := filepath.Join(ws, "bridge-token")
	if err := os.WriteFile(token, []byte("bridge-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	swappable := filepath.Join(ws, "notes.txt")
	if err := os.WriteFile(swappable, []byte("ordinary notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZERO_DAEMON_REMOTE_TOKEN", "")
	t.Setenv("ZERO_DAEMON_REMOTE_TOKEN_FILE", token)

	registry := NewRegistry()
	registry.Register(NewScopedReadFileTool(ws, nil))

	before := registry.Run(context.Background(), "read_file", map[string]any{"path": "notes.txt"})
	if before.Status != StatusOK || !strings.Contains(before.Output, "ordinary notes") {
		t.Fatalf("ordinary read before the swap failed: %+v", before)
	}

	// The attacker's move: atomically replace the already-served ordinary file
	// with a hard link to the protected token. A pre-open pathname check taken
	// before this point would still describe the ordinary file; only a check
	// bound to the NEXT open's own handle can see the swap.
	if err := os.Remove(swappable); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(token, swappable); err != nil {
		t.Skipf("workspace filesystem is not hard-linkable: %v", err)
	}

	after := registry.Run(context.Background(), "read_file", map[string]any{"path": "notes.txt"})
	if after.Status == StatusOK || strings.Contains(after.Output, "bridge-secret") {
		t.Fatalf("read_file served the token through a path swapped after an earlier successful read: %+v", after)
	}
}
