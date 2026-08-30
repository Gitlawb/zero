package installtxn

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitDirRestoresPreviousInstallWhenPublishFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := StageDir(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "version"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	publishErr := errors.New("publish failed")
	err = CommitDir(target, staged, func() error { return publishErr })
	if !errors.Is(err, publishErr) {
		t.Fatalf("CommitDir error = %v, want publish failure", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "version"))
	if err != nil {
		t.Fatalf("read restored install: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("restored content = %q, want old", data)
	}
}

func TestCommitDirRemovesFirstInstallWhenPublishFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "demo")
	staged, cleanup, err := StageDir(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}

	err = CommitDir(target, staged, func() error { return errors.New("publish failed") })
	if err == nil {
		t.Fatal("CommitDir unexpectedly succeeded")
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed first install remains at target: %v", statErr)
	}
}

func TestCleanupWorkspacePreservesRetainedPreviousInstall(t *testing.T) {
	workspace := t.TempDir()
	previous := filepath.Join(workspace, "previous")
	if err := os.MkdirAll(previous, 0o755); err != nil {
		t.Fatal(err)
	}

	cleanupWorkspace(workspace)

	if _, err := os.Stat(previous); err != nil {
		t.Fatalf("cleanup removed retained previous install: %v", err)
	}
}

// A retained backup is only recoverable if something can tell which install it
// came from, so CommitDir records the target before it moves anything. publish
// runs while the workspace is still in place, which is where that is visible.
func TestCommitDirRecordsItsTargetForRecovery(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := StageDir(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Dir(staged)

	var marker string
	var markerErr error
	if err := CommitDir(target, staged, func() error {
		data, err := os.ReadFile(filepath.Join(workspace, targetFileName))
		marker, markerErr = string(data), err
		return nil
	}); err != nil {
		t.Fatalf("CommitDir: %v", err)
	}

	if markerErr != nil {
		t.Fatalf("CommitDir left no way to attribute its backup: %v", markerErr)
	}
	if marker != "demo" {
		t.Fatalf("recorded target = %q, want %q", marker, "demo")
	}
}

// plantInterruptedCommit builds what a process killed between CommitDir's two
// renames leaves in dir: a workspace naming its target, the live tree moved into
// the backup beside it, and nothing at the target.
func plantInterruptedCommit(t *testing.T, dir, name, recorded, content string) string {
	t.Helper()
	target := filepath.Join(dir, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	staged, _, err := StageDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Dir(staged)
	if err := os.WriteFile(filepath.Join(workspace, targetFileName), []byte(recorded), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, filepath.Join(workspace, "previous")); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestRecoverPutsBackAnInterruptedCommit(t *testing.T) {
	dir := t.TempDir()
	workspace := plantInterruptedCommit(t, dir, "demo", "demo", "old")

	Recover(dir)

	data, err := os.ReadFile(filepath.Join(dir, "demo", "version"))
	if err != nil || string(data) != "old" {
		t.Fatalf("the interrupted install was not put back: got %q err %v", data, err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Errorf("the recovered workspace should be cleared, got %v", err)
	}
}

// A backup is a leftover, never a replacement for whatever is at the target now,
// empty or not. Go's os.Rename refuses an existing directory either way on the
// platforms tested, but POSIX allows replacing an empty one, so this pins the
// behavior rather than one syscall's take on it.
func TestRecoverLeavesALiveInstallAlone(t *testing.T) {
	for _, tc := range []struct{ name, live string }{
		{"empty install", ""},
		{"populated install", "live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			workspace := plantInterruptedCommit(t, dir, "demo", "demo", "old")
			live := filepath.Join(dir, "demo")
			if err := os.MkdirAll(live, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.live != "" {
				if err := os.WriteFile(filepath.Join(live, "version"), []byte(tc.live), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			Recover(dir)

			data, err := os.ReadFile(filepath.Join(live, "version"))
			if tc.live == "" {
				if err == nil {
					t.Fatalf("an existing install was replaced by a backup: version = %q", data)
				}
			} else if err != nil || string(data) != tc.live {
				t.Fatalf("the live install must win: got %q err %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(workspace, "previous", "version")); err != nil {
				t.Errorf("a backup it did not restore must be left intact: %v", err)
			}
		})
	}
}

// The recorded target names a directory inside the install root and nothing
// else. A name that could resolve anywhere is refused, not restored over.
func TestRecoverRefusesATargetOutsideTheInstallRoot(t *testing.T) {
	for _, recorded := range []string{"..", ".", "", "  ", "../escape", "a/b", string(filepath.Separator) + "etc"} {
		t.Run(recorded, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "installs")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(root, "escape")
			workspace := plantInterruptedCommit(t, dir, "demo", recorded, "old")

			Recover(dir)

			if _, err := os.Stat(outside); !os.IsNotExist(err) {
				t.Errorf("recovery wrote outside the install root: %v", err)
			}
			if _, err := os.Stat(filepath.Join(workspace, "previous", "version")); err != nil {
				t.Errorf("an unattributable backup must be left intact: %v", err)
			}
		})
	}
}

// A workspace mid-transaction has no backup yet, and one whose marker never got
// written cannot be attributed. Neither is something to act on, and neither is
// something to delete.
func TestRecoverSkipsWorkspacesItCannotActOn(t *testing.T) {
	dir := t.TempDir()
	noBackup, _, err := StageDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(noBackup, 0o755); err != nil {
		t.Fatal(err)
	}
	noMarker := plantInterruptedCommit(t, dir, "demo", "demo", "old")
	if err := os.Remove(filepath.Join(noMarker, targetFileName)); err != nil {
		t.Fatal(err)
	}

	Recover(dir)

	if _, err := os.Stat(noBackup); err != nil {
		t.Errorf("a workspace with no backup must be left alone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(noMarker, "previous", "version")); err != nil {
		t.Errorf("a backup with no marker must be left intact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been restored, got %v", err)
	}
}

// Recovery identifies a workspace by the name its own StageDir gives one. An
// installed tree that happens to contain the same two entries is not a
// workspace, and consuming it would destroy installed content.
func TestRecoverIgnoresAnInstallThatLooksLikeAWorkspace(t *testing.T) {
	dir := t.TempDir()
	lookalike := filepath.Join(dir, "demo")
	if err := os.MkdirAll(filepath.Join(lookalike, "previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lookalike, "previous", "version"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lookalike, targetFileName), []byte("elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}

	Recover(dir)

	if _, err := os.Stat(filepath.Join(lookalike, "previous", "version")); err != nil {
		t.Fatalf("recovery consumed installed content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "elsewhere")); !os.IsNotExist(err) {
		t.Errorf("recovery published from a directory that is not its workspace: %v", err)
	}
}
