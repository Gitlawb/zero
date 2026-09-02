package installtxn

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
// A tree at the target also says the publish rename committed, so the backup
// beside it holds what that install replaced and is retired rather than kept:
// keeping it let a later removal of this live tree hand the stale copy to the
// next recovery.
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
			if _, err := os.Stat(workspace); !os.IsNotExist(err) {
				t.Errorf("the superseded workspace should be retired, got %v", err)
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

// plantPublishedCommit builds what a process killed after CommitDir's second
// rename leaves in dir: the replacement live at the target, and the tree it
// replaced still sitting in the workspace beside it.
func plantPublishedCommit(t *testing.T, dir, name, recorded, published, superseded string) string {
	t.Helper()
	workspace := plantInterruptedCommit(t, dir, name, recorded, superseded)
	target := filepath.Join(dir, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version"), []byte(published), 0o644); err != nil {
		t.Fatal(err)
	}
	return workspace
}

// A backup left beside a target that the publish rename already replaced is
// superseded, not pending. Keeping it made a removal reversible by accident:
// the removal deleted the live target, and the next recovery then read the
// absent target as an interrupted swap and published the stale tree again.
func TestRecoverRetiresABackupASuccessfulPublishSuperseded(t *testing.T) {
	dir := t.TempDir()
	workspace := plantPublishedCommit(t, dir, "demo", "demo", "new", "old")
	target := filepath.Join(dir, "demo")

	Recover(dir)

	data, err := os.ReadFile(filepath.Join(target, "version"))
	if err != nil || string(data) != "new" {
		t.Fatalf("the published install must be left alone: got %q err %v", data, err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("the superseded workspace should be retired, got %v", err)
	}

	if err := RemoveDir(target, func() error { return nil }); err != nil {
		t.Fatalf("RemoveDir: %v", err)
	}
	Recover(dir)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a removed install was resurrected by recovery: %v", err)
	}
}

// A rollback must never leave a partial tree at the target, because recovery
// reads a present target as proof the publish committed and retires the backup
// beside it. Deleting the failed install in place opened exactly that window:
// a kill partway through the delete left a husk at the target while the backup
// was still the only complete copy. Moving the failed tree aside first closes
// it. The permission trick makes the in-place delete fail partway on demand.
func TestCommitDirRollbackNeverLeavesAPartialTargetTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block removal on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
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
	locked := filepath.Join(staged, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "held"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})

	publishErr := errors.New("publish failed")
	if err := CommitDir(target, staged, func() error { return publishErr }); !errors.Is(err, publishErr) {
		t.Fatalf("CommitDir error = %v, want publish failure", err)
	}

	data, err := os.ReadFile(filepath.Join(target, "version"))
	if err != nil || string(data) != "old" {
		t.Fatalf("the previous install must be back at the target: got %q err %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(target, "locked")); !os.IsNotExist(err) {
		t.Fatalf("part of the failed install was left at the target: %v", err)
	}
}

// The window between rollback's two renames leaves the target absent, the
// backup intact, and the failed install set aside beside it. That is the same
// pre-publish shape recovery already restores, and the set-aside tree must not
// change its reading of it.
func TestRecoverPutsBackAnInterruptedRollback(t *testing.T) {
	dir := t.TempDir()
	workspace := plantInterruptedCommit(t, dir, "demo", "demo", "old")
	failed := filepath.Join(workspace, "failed")
	if err := os.MkdirAll(failed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failed, "version"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	Recover(dir)

	data, err := os.ReadFile(filepath.Join(dir, "demo", "version"))
	if err != nil || string(data) != "old" {
		t.Fatalf("the interrupted rollback was not put back: got %q err %v", data, err)
	}
}

// The move aside can itself fail, and then the failed install is still live at
// the target with the backup still the only copy of what the user had. Recovery
// reads a target that is there as a committed publish, so a workspace left
// attributable here would have its backup retired: the one state where the new
// retire branch would destroy a tree that was never superseded. Dropping the
// marker hands it to the guard that already leaves unattributable workspaces
// alone. The parent permissions make the move aside fail on demand.
func TestRollbackKeepsABackupItCouldNotRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block renames on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
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
	workspace := filepath.Dir(staged)
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	publishErr := errors.New("publish failed")
	// The install root goes read only after the publish rename, so rollback
	// cannot move the failed install off the target.
	err = CommitDir(target, staged, func() error {
		if err := os.Chmod(root, 0o555); err != nil {
			t.Fatal(err)
		}
		return publishErr
	})
	if !errors.Is(err, publishErr) {
		t.Fatalf("CommitDir error = %v, want publish failure", err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}

	Recover(root)

	data, err := os.ReadFile(filepath.Join(workspace, "previous", "version"))
	if err != nil || string(data) != "old" {
		t.Fatalf("a backup rollback could not restore must be kept: got %q err %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(target, "version"))
	if err != nil || string(data) != "new" {
		t.Fatalf("recovery must leave the tree at the target alone: got %q err %v", data, err)
	}
}
