package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedFileDiffRefusesPartialOrBinaryContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if diff, ok := boundedFileDiff(path, "old", "new", true, true); !ok || diff.Path != path || !diff.OldExists || !diff.NewExists || diff.OldText != "old" || diff.NewText != "new" {
		t.Fatalf("small text diff = %#v, %t", diff, ok)
	}
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"unchanged", "same", "same"},
		{"binary old", string([]byte{0xff}), "text"},
		{"binary new", "text", string([]byte{0xff})},
		{"nul old", "token=sk-proj-abc\x00def", "text"},
		{"escape new", "text", "token=sk-proj-abc\x1bdef"},
		{"c1 old", "token=sk-proj-abc\u0085def", "text"},
		{"too large", strings.Repeat("a", maxToolPreviewBytes), "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diff, ok := boundedFileDiff(path, tc.old, tc.new, true, true); ok || diff != (FileDiff{}) {
				t.Fatalf("unexpected diff = %#v, %t", diff, ok)
			}
		})
	}
}

func TestBoundedFileDiffPreservesEmptyFileOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	for _, tc := range []struct {
		name                 string
		oldExists, newExists bool
	}{
		{name: "create", newExists: true},
		{name: "delete", oldExists: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diff, ok := boundedFileDiff(path, "", "", tc.oldExists, tc.newExists)
			if !ok || diff.OldExists != tc.oldExists || diff.NewExists != tc.newExists {
				t.Fatalf("empty %s = %#v, %t", tc.name, diff, ok)
			}
		})
	}
}

func TestStructuredPatchFileDiffsPreserveEmptyOperationsAndResultBudget(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changes := []structuredPatchChange{
		{kind: structuredPatchAdd, to: structuredPatchTarget{absolute: filepath.Join(root, "created"), relative: "created"}},
		{kind: structuredPatchDelete, from: structuredPatchTarget{absolute: filepath.Join(root, "deleted"), relative: "deleted"}},
		{kind: structuredPatchUpdate, from: structuredPatchTarget{absolute: filepath.Join(root, "from"), relative: "from"}, to: structuredPatchTarget{absolute: filepath.Join(root, "to"), relative: "to"}},
	}
	diffs := fileDiffsFromStructuredPatch(".", changes)
	if len(diffs) != 4 {
		t.Fatalf("empty create/delete/move diffs = %#v", diffs)
	}
	for _, diff := range diffs {
		if diff.Path == "" || (!diff.OldExists && !diff.NewExists) {
			t.Fatalf("invalid diff = %#v", diff)
		}
	}

	large := strings.Repeat("x", 20*1024)
	budgeted := fileDiffsFromStructuredPatch(".", []structuredPatchChange{
		{kind: structuredPatchAdd, to: structuredPatchTarget{absolute: filepath.Join(root, "one")}, after: large},
		{kind: structuredPatchAdd, to: structuredPatchTarget{absolute: filepath.Join(root, "two")}, after: large},
		{kind: structuredPatchAdd, to: structuredPatchTarget{absolute: filepath.Join(root, "three")}, after: large},
	})
	if len(budgeted) != 2 {
		t.Fatalf("aggregate file-diff budget = %d diffs, want 2", len(budgeted))
	}
}
