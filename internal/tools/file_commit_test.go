package tools

import (
	"context"
	"errors"
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

func installFileCreateRace(t *testing.T, mutate func(string)) {
	t.Helper()
	prior := fileCreateBeforeExclusivePublish
	fileCreateBeforeExclusivePublish = mutate
	t.Cleanup(func() { fileCreateBeforeExclusivePublish = prior })
}

// The window named by the review: the path was observed missing by
// commitFileContents's own Lstat, then a file appeared before the exclusive
// publication. The creation must be refused, not silently converted into an
// overwrite.
func TestCommitFileContentsRefusesCreateRaceAfterObservationLstat(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "created.txt")
	installFileCreateRace(t, func(path string) {
		if err := os.WriteFile(path, []byte("other writer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})

	warning, err := commitFileContents(target, nil, nil, "zero\n")
	if err == nil || !errors.Is(err, errFileChangedDuringWrite) {
		t.Fatalf("raced create = warning %q, error %v; want errFileChangedDuringWrite", warning, err)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "other writer\n" {
		t.Fatalf("raced create content = %q, err=%v", got, readErr)
	}
}

// commitFileContents must bind its publication to the object it validated. A
// symlink observed as its target (what the old Stat-based observation produced)
// must be refused, leaving both the link and the pointed-to file untouched.
func TestCommitFileContentsRefusesSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("target bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	observed, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := commitFileContents(link, observed, strPtr("target bytes\n"), "new bytes\n"); err == nil {
		t.Fatal("commit must refuse a symlink destination")
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink destination was replaced by a regular file")
	}
	if got, err := os.ReadFile(real); err != nil || string(got) != "target bytes\n" {
		t.Fatalf("symlink target mutated: %q, err=%v", got, err)
	}
}

func installFileWriteStat(t *testing.T, stat func(*os.File) (os.FileInfo, error)) {
	t.Helper()
	prior := fileWriteStat
	fileWriteStat = stat
	t.Cleanup(func() { fileWriteStat = prior })
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

func TestOverwriteDoesNotStatOpenedFileAfterFinalPreimageComparison(t *testing.T) {
	for name, run := range map[string]func(string) Result{
		"write overwrite": func(root string) Result {
			return NewScopedWriteFileTool(root, nil).Run(context.Background(), map[string]any{
				"path": "existing.txt", "content": "zero\n", "overwrite": true,
			})
		},
		"edit": func(root string) Result {
			return NewScopedEditFileTool(root, nil).Run(context.Background(), map[string]any{
				"path": "existing.txt", "old_string": "observed", "new_string": "zero",
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "existing.txt")
			if err := os.WriteFile(target, []byte("observed\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			statCalls := 0
			installFileWriteStat(t, func(file *os.File) (os.FileInfo, error) {
				statCalls++
				if statCalls == 2 {
					// This preserves the inode, so identity-only checks cannot detect it.
					if err := os.WriteFile(target, []byte("other writer\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return file.Stat()
			})

			result := run(root)
			if result.Status != StatusOK {
				t.Fatalf("overwrite status = %s: %s", result.Status, result.Output)
			}
			if statCalls != 1 {
				t.Fatalf("opened file was statted %d times; the final byte comparison must be followed directly by mutation", statCalls)
			}
		})
	}
}
