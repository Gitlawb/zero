package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return DefaultPaths(root)
}

func TestANoteRoundTripsThroughItsScope(t *testing.T) {
	paths := testPaths(t)
	for _, scope := range []Scope{ScopeProject, ScopeLocal} {
		if _, err := Write(paths, scope, "conventions", "how this repo does errors", "Wrap with %w.\n"); err != nil {
			t.Fatalf("%s: write: %v", scope, err)
		}
		note, err := Read(paths, scope, "conventions")
		if err != nil {
			t.Fatalf("%s: read: %v", scope, err)
		}
		if note.Description != "how this repo does errors" {
			t.Errorf("%s: description = %q", scope, note.Description)
		}
		if !strings.Contains(note.Body, "Wrap with %w.") {
			t.Errorf("%s: body = %q", scope, note.Body)
		}
		if note.Scope != scope {
			t.Errorf("scope = %q, want %q", note.Scope, scope)
		}
	}
}

// BOTH SCOPES ARE LISTED. Unlike saved plans, where project shadows user, these
// hold different KINDS of thing — hiding one behind the other would lose a note
// rather than resolve a conflict.
func TestListingShowsBothScopesWithoutShadowing(t *testing.T) {
	paths := testPaths(t)
	if _, err := Write(paths, ScopeProject, "shared", "team convention", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(paths, ScopeLocal, "shared", "my own note", "y"); err != nil {
		t.Fatal(err)
	}
	notes := List(paths)
	if len(notes) != 2 {
		t.Fatalf("listed %d notes, want both scopes: %+v", len(notes), notes)
	}
	if notes[0].Scope != ScopeProject || notes[1].Scope != ScopeLocal {
		t.Errorf("project must be listed first: %+v", notes)
	}
}

// THE NAME IS THE PATH GUARD, an allow-list for the same reason plan names use
// one: every deny-list in this repo has leaked at least once.
func TestNamesCannotTraverse(t *testing.T) {
	paths := testPaths(t)
	for _, name := range []string{
		"../escape", "..", ".", "a/b", `a\b`, "a b", "", strings.Repeat("x", 65),
		"~/evil", "a;b", "a\x00b", "note.md",
	} {
		if _, err := Write(paths, ScopeProject, name, "d", "b"); !errors.Is(err, ErrBadName) {
			t.Errorf("Write accepted %q: %v", name, err)
		}
		if _, err := Read(paths, ScopeProject, name); !errors.Is(err, ErrBadName) {
			t.Errorf("Read accepted %q: %v", name, err)
		}
	}
	for _, name := range []string{"conventions", "decision-2", "audit_findings", "A1"} {
		if _, err := Write(paths, ScopeProject, name, "d", "b"); err != nil {
			t.Errorf("Write rejected %q: %v", name, err)
		}
	}
}

// A SYMLINK IS REFUSED. A note store is a write primitive pointed at a path the
// model chooses, which is the same shape as "save my plan" and needs the same
// answer.
func TestWritingRefusesToFollowASymlink(t *testing.T) {
	paths := testPaths(t)
	base := filepath.Dir(paths.ProjectDir)
	if err := os.MkdirAll(paths.ProjectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "precious")
	if err := os.WriteFile(target, []byte("do not clobber"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(paths.ProjectDir, "evil"+fileExt)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Write(paths, ScopeProject, "evil", "d", "b"); !errors.Is(err, ErrIsSymlink) {
		t.Fatalf("Write followed a symlink: %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "do not clobber" {
		t.Fatalf("the symlink target was overwritten: %q", body)
	}
}

// An unavailable scope is refused with a reason rather than written elsewhere.
func TestAnUnavailableScopeIsRefused(t *testing.T) {
	if _, err := Write(Paths{}, ScopeProject, "x", "d", "b"); !errors.Is(err, ErrNoStore) {
		t.Errorf("a write with no store configured returned %v", err)
	}
	if _, err := Write(testPaths(t), Scope("elsewhere"), "x", "d", "b"); !errors.Is(err, ErrBadScope) {
		t.Errorf("an unknown scope returned %v", err)
	}
}

// A note written by hand, without frontmatter, must still be readable — losing
// it would be the store punishing the reader it exists to serve.
func TestAHandWrittenNoteWithoutFrontmatterStillReads(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.ProjectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ProjectDir, "manual"+fileExt), []byte("just some prose\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	note, err := Read(paths, ScopeProject, "manual")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(note.Body, "just some prose") {
		t.Errorf("body = %q", note.Body)
	}
}

func TestOversizedNotesAreRefusedAndForgetIsIdempotent(t *testing.T) {
	paths := testPaths(t)
	if _, err := Write(paths, ScopeProject, "big", "d", strings.Repeat("x", maxNoteBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversized note was accepted: %v", err)
	}
	if err := Forget(paths, ScopeProject, "never-existed"); err != nil {
		t.Errorf("forgetting a missing note errored: %v", err)
	}
	if _, err := Write(paths, ScopeLocal, "temp", "d", "b"); err != nil {
		t.Fatal(err)
	}
	if err := Forget(paths, ScopeLocal, "temp"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if _, err := Read(paths, ScopeLocal, "temp"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a forgotten note is still readable: %v", err)
	}
}
