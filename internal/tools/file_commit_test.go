package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installFileWriteRace(t *testing.T, mutate func(string)) {
	t.Helper()
	prior := fileWriteBeforeCommit
	fileWriteBeforeCommit = mutate
	t.Cleanup(func() { fileWriteBeforeCommit = prior })
}

func TestWriteFileRefusesCreateAndOverwriteRaces(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "created.txt")
		installFileWriteRace(t, func(path string) {
			if err := os.WriteFile(path, []byte("other writer\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		result := NewScopedWriteFileTool(root, nil).Run(context.Background(), map[string]any{
			"path": "created.txt", "content": "zero\n",
		})
		if result.Status != StatusError {
			t.Fatalf("raced create status = %s, want error", result.Status)
		}
		if got, err := os.ReadFile(target); err != nil || string(got) != "other writer\n" {
			t.Fatalf("raced create content = %q, err=%v", got, err)
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "existing.txt")
		if err := os.WriteFile(target, []byte("observed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		installFileWriteRace(t, func(path string) {
			if err := os.WriteFile(path, []byte("other writer\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		result := NewScopedWriteFileTool(root, nil).Run(context.Background(), map[string]any{
			"path": "existing.txt", "content": "zero\n", "overwrite": true,
		})
		if result.Status != StatusError || !strings.Contains(result.Output, errFileChangedDuringWrite.Error()) {
			t.Fatalf("raced overwrite = %s: %s", result.Status, result.Output)
		}
		if got, err := os.ReadFile(target); err != nil || string(got) != "other writer\n" {
			t.Fatalf("raced overwrite content = %q, err=%v", got, err)
		}
	})
}

func TestEditFileRefusesPreimageRace(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("observed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installFileWriteRace(t, func(path string) {
		if err := os.WriteFile(path, []byte("other writer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	result := NewScopedEditFileTool(root, nil).Run(context.Background(), map[string]any{
		"path": "existing.txt", "old_string": "observed", "new_string": "zero",
	})
	if result.Status != StatusError || !strings.Contains(result.Output, errFileChangedDuringWrite.Error()) {
		t.Fatalf("raced edit = %s: %s", result.Status, result.Output)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "other writer\n" {
		t.Fatalf("raced edit content = %q, err=%v", got, err)
	}
}
