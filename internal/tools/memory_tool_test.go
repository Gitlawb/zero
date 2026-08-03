package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/memory"
)

func memoryPaths(t *testing.T) memory.Paths {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return memory.DefaultPaths(root)
}

// READ FREELY, WRITE ON APPROVAL. Reading a note is reading a file the user
// already has. Writing one puts text into their repo that every future session
// reads and believes, so it prompts like any other write.
func TestTheMemoryToolsSplitReadFromWrite(t *testing.T) {
	read := NewMemoryTool(memoryPaths(t))
	if got := read.Safety().Permission; got != PermissionAllow {
		t.Errorf("reading a note prompts (%v); it is no more than a read_file", got)
	}
	if got := read.Safety().SideEffect; got != SideEffectRead {
		t.Errorf("read side effect = %v", got)
	}
	write := NewMemoryWriteTool(memoryPaths(t))
	if got := write.Safety().Permission; got != PermissionPrompt {
		t.Errorf("saving a note does not prompt (%v); a future session will read it and believe it", got)
	}
	if got := write.Safety().SideEffect; got != SideEffectWrite {
		t.Errorf("write side effect = %v", got)
	}
}

func TestANoteSavedIsANoteListedAndRead(t *testing.T) {
	paths := memoryPaths(t)
	write, read := NewMemoryWriteTool(paths), NewMemoryTool(paths)

	res := write.Run(context.Background(), map[string]any{
		"name": "errors", "description": "how this repo wraps errors", "content": "Always %w.",
	})
	if res.Status != StatusOK {
		t.Fatalf("write: %s", res.Output)
	}

	listed := read.Run(context.Background(), map[string]any{})
	if !strings.Contains(listed.Output, "errors") || !strings.Contains(listed.Output, "how this repo wraps errors") {
		t.Fatalf("the listing does not show the note and its description:\n%s", listed.Output)
	}
	// The description is what makes a listing useful — a reader chooses what to
	// open instead of reading everything.
	if strings.Contains(listed.Output, "Always %w.") {
		t.Errorf("the listing dumped the body; it exists so the body need not be read:\n%s", listed.Output)
	}

	one := read.Run(context.Background(), map[string]any{"name": "errors"})
	if !strings.Contains(one.Output, "Always %w.") {
		t.Fatalf("reading by name did not return the body:\n%s", one.Output)
	}
}

// LOCAL BY DEFAULT. A note the model chose to keep must not land in a shared,
// checked-in file unless someone said so.
func TestANoteDefaultsToTheLocalScope(t *testing.T) {
	paths := memoryPaths(t)
	res := NewMemoryWriteTool(paths).Run(context.Background(), map[string]any{"name": "n", "content": "x"})
	if res.Status != StatusOK {
		t.Fatalf("write: %s", res.Output)
	}
	if !strings.Contains(res.Output, "local") {
		t.Errorf("a scopeless write did not go local: %q", res.Output)
	}
	if _, err := memory.Read(paths, memory.ScopeProject, "n"); err == nil {
		t.Error("a scopeless write landed in the shared, checked-in scope")
	}
}

// Deleting is asking for it to be gone, so a missing note is not an error.
func TestOmittingContentForgetsTheNote(t *testing.T) {
	paths := memoryPaths(t)
	write, read := NewMemoryWriteTool(paths), NewMemoryTool(paths)
	write.Run(context.Background(), map[string]any{"name": "temp", "content": "x"})
	if res := write.Run(context.Background(), map[string]any{"name": "temp"}); res.Status != StatusOK {
		t.Fatalf("forget: %s", res.Output)
	}
	if res := read.Run(context.Background(), map[string]any{"name": "temp"}); res.Status != StatusError {
		t.Errorf("a forgotten note is still readable: %s", res.Output)
	}
	if res := write.Run(context.Background(), map[string]any{"name": "never-existed"}); res.Status != StatusOK {
		t.Errorf("forgetting a missing note errored: %s", res.Output)
	}
}

// Memory switched off says so, rather than reporting an empty store — "you have
// no notes" and "notes are off here" are different answers.
func TestMemoryUnavailableSaysSo(t *testing.T) {
	res := NewMemoryTool(memory.Paths{}).Run(context.Background(), map[string]any{})
	if res.Status != StatusError || !strings.Contains(res.Output, "not available") {
		t.Errorf("an unconfigured store did not say so: %+v", res)
	}
}
