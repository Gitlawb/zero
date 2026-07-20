package execution

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestChangeObserverReportsCreateModifyAndDelete(t *testing.T) {
	root := t.TempDir()
	modified := filepath.Join(root, "modified.txt")
	deleted := filepath.Join(root, "deleted.txt")
	if err := os.WriteFile(modified, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deleted, []byte("delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	observer := NewChangeObserver(root)
	if err := os.WriteFile(modified, []byte("after!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "created.ts"), []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Path: "deleted.txt", Kind: ChangeDeleted},
		{Path: "modified.txt", Kind: ChangeModified},
		{Path: "src/created.ts", Kind: ChangeCreated},
	}
	if got := observer.Changes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes() = %#v, want %#v", got, want)
	}
}

func TestChangeObserverSkipsControlMetadataAndSummarizesGeneratedTrees(t *testing.T) {
	root := t.TempDir()
	observer := NewChangeObserver(root)
	for _, path := range []string{
		filepath.Join(root, ".zero", "config.json"),
		filepath.Join(root, ".agents", "skills", "x"),
		filepath.Join(root, ".git", "config"),
		filepath.Join(root, "node_modules", "pkg", "index.js"),
		filepath.Join(root, "dist", "bundle.js"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("generated"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := []Change{
		{Path: "dist/", Kind: ChangeCreated, Aggregated: true, Count: 1},
		{Path: "node_modules/", Kind: ChangeCreated, Aggregated: true, Count: 1},
	}
	if got := observer.Changes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes() = %#v, want generated summaries without control metadata %#v", got, want)
	}
}
