package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/fscopy"
)

// initGitPluginRepo creates a real local git repo holding a plugin and returns a
// file:// URL, exercising the DEFAULT git runner end to end. Skipped when git is
// unavailable.
func initGitPluginRepo(t *testing.T, manifest map[string]any) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	writeSourcePlugin(t, repo, manifest)
	run("add", "-A")
	run("commit", "-qm", "init")
	return "file://" + repo
}

func TestInstallFromRealLocalGitRepo(t *testing.T) {
	destDir := t.TempDir()
	url := initGitPluginRepo(t, validManifest())

	result, err := Install(context.Background(), InstallOptions{Source: url, Dir: destDir})
	if err != nil {
		t.Fatalf("Install from git: %v", err)
	}
	if result.ID != "zero.demo" {
		t.Fatalf("ID = %q, want zero.demo", result.ID)
	}
	loaded, err := Load(LoadOptions{Roots: []Root{{Source: SourceUser, Path: destDir}}})
	if err != nil || len(loaded.Plugins) != 1 {
		t.Fatalf("installed git plugin not discoverable: err=%v plugins=%#v", err, loaded.Plugins)
	}
	// copyTree must skip .git so clone metadata never lands in the plugins dir.
	if _, err := os.Stat(filepath.Join(destDir, "zero.demo", ".git")); err == nil {
		t.Fatalf(".git metadata must not be copied into the plugins dir")
	}
}

func writeSourcePlugin(t *testing.T, dir string, manifest map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFileName), data, 0o644); err != nil {
		t.Fatalf("write plugin.json: %v", err)
	}
	return dir
}

func validManifest() map[string]any {
	return map[string]any{
		"schemaVersion": float64(1),
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
		"description":   "Demo plugin",
	}
}

func TestInstallCopiesLocalPluginAndRecordsHash(t *testing.T) {
	destDir := t.TempDir()
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())

	result, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if result.ID != "zero.demo" {
		t.Fatalf("ID = %q, want zero.demo", result.ID)
	}
	if result.Hash == "" {
		t.Fatalf("expected a recorded content hash")
	}

	// The installed manifest is discoverable through the loader.
	loaded, err := Load(LoadOptions{Roots: []Root{{Source: SourceUser, Path: destDir}}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Plugins) != 1 || loaded.Plugins[0].ID != "zero.demo" {
		t.Fatalf("installed plugin not discoverable: %#v", loaded.Plugins)
	}

	// Lockfile records id -> source + hash.
	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if entries["zero.demo"].Hash != result.Hash || entries["zero.demo"].Source != canonicalSource(src) {
		t.Fatalf("lockfile entry unexpected: %#v", entries["zero.demo"])
	}
	installedHash, err := fscopy.HashTree(filepath.Join(destDir, "zero.demo"))
	if err != nil {
		t.Fatalf("hash installed plugin: %v", err)
	}
	if result.Hash != installedHash {
		t.Fatalf("result hash %q does not match installed tree hash %q", result.Hash, installedHash)
	}
}

func TestInstallRejectsSymlinkedManifestSkippedByCopy(t *testing.T) {
	destDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	realManifestDir := t.TempDir()
	realManifest := filepath.Join(realManifestDir, manifestFileName)
	data, err := json.MarshalIndent(validManifest(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realManifest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realManifest, filepath.Join(src, manifestFileName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err = Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err == nil {
		t.Fatalf("expected symlinked manifest skipped by copy to fail install")
	}
	if !strings.Contains(err.Error(), "staged plugin.json missing") {
		t.Fatalf("error = %v, want staged manifest missing", err)
	}
	if entries, _ := os.ReadDir(destDir); len(entries) != 0 {
		t.Fatalf("failed install must not write into dest, found: %#v", entries)
	}
}

func TestInstallRejectsInvalidManifest(t *testing.T) {
	destDir := t.TempDir()
	// Missing required fields (id/name/version) -> ParseManifest rejects it.
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "bad"), map[string]any{
		"schemaVersion": float64(1),
	})

	_, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err == nil {
		t.Fatalf("expected an error for an invalid manifest")
	}
	if entries, _ := os.ReadDir(destDir); len(entries) != 0 {
		t.Fatalf("invalid manifest must not write into dest, found: %#v", entries)
	}
}

func TestInstallRejectsMissingManifest(t *testing.T) {
	destDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir}); err == nil {
		t.Fatalf("expected an error for a source without plugin.json")
	}
}

func TestInstallNeverExecutesInstallScript(t *testing.T) {
	destDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "PWNED")
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())
	// Drop a hostile install script alongside the manifest. Install must copy the
	// plugin tree verbatim but NEVER run anything.
	script := filepath.Join(src, "install.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("install must never execute an install script")
	}
}

func TestInstallReinstallShowsHashChange(t *testing.T) {
	destDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	writeSourcePlugin(t, src, validManifest())

	first, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	bumped := validManifest()
	bumped["version"] = "0.2.0"
	writeSourcePlugin(t, src, bumped)
	second, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if !second.Updated {
		t.Fatalf("reinstall with changed content should be flagged as an update")
	}
	if second.PreviousHash != first.Hash || second.Hash == first.Hash {
		t.Fatalf("expected a hash change: prev=%q first=%q new=%q", second.PreviousHash, first.Hash, second.Hash)
	}
}

func TestConcurrentInstallsPreserveEveryLockEntry(t *testing.T) {
	destDir := t.TempDir()
	const count = 12
	sources := make([]string, count)
	for index := range count {
		manifest := validManifest()
		manifest["id"] = fmt.Sprintf("zero.concurrent.%02d", index)
		sources[index] = writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), manifest)
	}

	errs := make(chan error, count)
	for _, source := range sources {
		go func() {
			_, err := Install(context.Background(), InstallOptions{Source: source, Dir: destDir})
			errs <- err
		}()
	}
	for range count {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Install: %v", err)
		}
	}

	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if len(entries) != count {
		t.Fatalf("lockfile has %d entries, want %d: %#v", len(entries), count, entries)
	}
}

// TestInstallReinstallDetectsNestedFileChange guards that the recorded hash
// covers the whole installed tree, not just plugin.json. A change to a tool
// script (with the manifest unchanged) must still be reported as an update so
// checksum pinning catches modified executable content.
func TestInstallReinstallDetectsNestedFileChange(t *testing.T) {
	destDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	writeSourcePlugin(t, src, map[string]any{
		"schemaVersion": float64(1),
		"id":            "zero.tool",
		"name":          "Tool",
		"version":       "0.1.0",
		"tools": []any{map[string]any{
			"name":    "lookup",
			"command": "node",
			"args":    []any{"tools/lookup.mjs"},
		}},
	})
	entryDir := filepath.Join(src, "tools")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(entryDir, "lookup.mjs")
	if err := os.WriteFile(entry, []byte("console.log('v1')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Change ONLY the nested tool script; the manifest is untouched.
	if err := os.WriteFile(entry, []byte("console.log('v2')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if !second.Updated {
		t.Fatalf("changing a nested file should be flagged as an update")
	}
	if second.PreviousHash != first.Hash || second.Hash == first.Hash {
		t.Fatalf("expected the hash to change: prev=%q first=%q new=%q", second.PreviousHash, first.Hash, second.Hash)
	}
}

func TestInstallNameClashWarnsAndDoesNotOverwriteWithoutForce(t *testing.T) {
	destDir := t.TempDir()
	srcA := writeSourcePlugin(t, filepath.Join(t.TempDir(), "a"), validManifest())
	if _, err := Install(context.Background(), InstallOptions{Source: srcA, Dir: destDir}); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	srcB := writeSourcePlugin(t, filepath.Join(t.TempDir(), "b"), validManifest())
	_, err := Install(context.Background(), InstallOptions{Source: srcB, Dir: destDir})
	if !errors.Is(err, ErrNameClash) {
		t.Fatalf("expected ErrNameClash from a different source, got %v", err)
	}

	if _, err := Install(context.Background(), InstallOptions{Source: srcB, Dir: destDir, Force: true}); err != nil {
		t.Fatalf("forced reinstall: %v", err)
	}
	entries, _ := ReadLock(destDir)
	if entries["zero.demo"].Source != canonicalSource(srcB) {
		t.Fatalf("forced overwrite did not update source: %#v", entries["zero.demo"])
	}
}

// TestInstallSameLocalSourceDifferentSpellingIsNotAClash verifies that a local
// source installed via an absolute path and re-installed via an equivalent
// relative spelling is treated as the same source, not a clash, because the
// recorded source is canonicalized.
func TestInstallSameLocalSourceDifferentSpellingIsNotAClash(t *testing.T) {
	destDir := t.TempDir()
	abs := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())

	if _, err := Install(context.Background(), InstallOptions{Source: abs, Dir: destDir}); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// A different textual spelling of the same directory (a redundant "/./"
	// segment) canonicalizes to the same absolute path, so it must not clash. (A
	// cwd-relative spelling can't be expressed across drives on Windows, where the
	// temp dir and the repo are on different volumes, so use a same-dir alternate.)
	messy := filepath.Dir(abs) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(abs)
	if _, err := Install(context.Background(), InstallOptions{Source: messy, Dir: destDir}); err != nil {
		t.Fatalf("reinstall with an equivalent spelling should not clash: %v", err)
	}

	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if entries["zero.demo"].Source != canonicalSource(abs) {
		t.Fatalf("lockfile should record the canonical source %q, got %q", canonicalSource(abs), entries["zero.demo"].Source)
	}
}

func TestInstallGitSourceUsesRunner(t *testing.T) {
	destDir := t.TempDir()
	used := false
	runner := func(ctx context.Context, destination string, source string) error {
		used = true
		writeSourcePlugin(t, destination, validManifest())
		return nil
	}
	result, err := Install(context.Background(), InstallOptions{
		Source:    "https://example.com/plugin.git",
		Dir:       destDir,
		GitRunner: runner,
	})
	if err != nil {
		t.Fatalf("install via runner: %v", err)
	}
	if !used {
		t.Fatalf("git runner not invoked for URL source")
	}
	if result.ID != "zero.demo" {
		t.Fatalf("ID = %q", result.ID)
	}
}

func TestRemoveDeletesPluginAndLockEntry(t *testing.T) {
	destDir := t.TempDir()
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir}); err != nil {
		t.Fatalf("install: %v", err)
	}

	if err := Remove(destDir, "zero.demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	loaded, _ := Load(LoadOptions{Roots: []Root{{Source: SourceUser, Path: destDir}}})
	if len(loaded.Plugins) != 0 {
		t.Fatalf("plugin still present after Remove: %#v", loaded.Plugins)
	}
	entries, _ := ReadLock(destDir)
	if _, ok := entries["zero.demo"]; ok {
		t.Fatalf("lockfile entry survived Remove")
	}
}

func TestRemoveUnknownPluginErrors(t *testing.T) {
	if err := Remove(t.TempDir(), "missing.plugin"); err == nil {
		t.Fatalf("expected an error removing an unknown plugin")
	}
}

// TestInstallCopiesEntireTree confirms install copies the whole plugin directory
// (entry scripts, prompt/skill files) so an installed plugin is actually runnable
// through Stage 09 activation — it just never executes any of it during install.
func TestInstallCopiesEntireTree(t *testing.T) {
	destDir := t.TempDir()
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": float64(1),
		"id":            "zero.tool",
		"name":          "Tool",
		"version":       "0.1.0",
		"tools": []any{map[string]any{
			"name":    "lookup",
			"command": "node",
			"args":    []any{"tools/lookup.mjs"},
		}},
	})
	entryDir := filepath.Join(src, "tools")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "lookup.mjs"), []byte("console.log('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	copied := filepath.Join(filepath.Dir(result.ManifestPath), "tools", "lookup.mjs")
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("entry script not copied into install dir: %v", err)
	}
}

// TestInstallDoesNotWipePreviousOnCopyFailure guards the atomic install: a
// reinstall whose COPY step fails must leave the previously installed plugin and
// its lockfile entry completely intact, rather than RemoveAll-ing the old tree
// and stranding the lockfile pointing at a deleted directory. We drive the
// package swap helper directly with a prior install present and force the copy
// to fail by passing a src whose CopyTree cannot run (a directory with a
// non-existent staging path), so copyAndSwapIntoPlace returns early before
// touching the previous install — the regression the atomic swap prevents is
// exactly the old RemoveAll(target)-before-copy, which would already be gone.
func TestInstallDoesNotWipePreviousOnCopyFailure(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "prior")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, manifestFileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	// staging does not exist: CopyTree cannot write into it, so the copy fails
	// and copyAndSwapIntoPlace returns before the previous install is touched.
	missingSrc := filepath.Join(parent, "src-does-not-exist")
	if err := copyAndSwapIntoPlace(missingSrc, parent, target); err == nil {
		t.Fatal("expected copyAndSwapIntoPlace to fail when the source copy fails")
	}

	// The previous install must survive the failed copy, content intact.
	data, err := os.ReadFile(filepath.Join(target, manifestFileName))
	if err != nil {
		t.Fatalf("previous install lost during failed copy: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("previous install content = %q, want %q", string(data), "old")
	}
	// No partial staging or backup dir left behind on the parent.
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".zero-plugin-install-") {
			t.Fatalf("leftover staging/backup dir after failed copy: %s", e.Name())
		}
	}
}

// TestRecoverPendingRollsBackInterruptedReplace simulates a kill after the
// first rename of a replace (old tree stashed under the deterministic backup
// name, target absent). RecoverPending must restore the canonical install so
// neither discovery nor a later install observes a missing target with the old
// tree stranded under a randomized backup name.
func TestRecoverPendingRollsBackInterruptedReplace(t *testing.T) {
	parent := t.TempDir()
	pluginsDir := filepath.Join(parent, "plugins")
	id := "zero.demo"
	target := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, manifestFileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate the crash: the swap moved the old tree aside and died before the
	// new tree landed. target is gone; the old tree sits under the deterministic
	// backup name.
	backup := filepath.Join(parent, swapPrefix+id)
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}

	if err := RecoverPending(pluginsDir); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, manifestFileName))
	if err != nil {
		t.Fatalf("previous install not restored: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("restored content = %q, want %q", string(data), "old")
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup must be consumed by recovery: %v", err)
	}
}

// TestRecoverPendingCommitsCompletedReplace simulates a crash AFTER both renames
// landed (new tree at target, old tree stranded under the backup name). Recovery
// must commit by dropping the now-superseded backup.
func TestRecoverPendingCommitsCompletedReplace(t *testing.T) {
	parent := t.TempDir()
	pluginsDir := filepath.Join(parent, "plugins")
	id := "zero.demo"
	target := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, manifestFileName), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(parent, swapPrefix+id)
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, manifestFileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RecoverPending(pluginsDir); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, manifestFileName))
	if err != nil {
		t.Fatalf("new install disturbed by recovery: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("new content = %q, want %q", string(data), "new")
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("superseded backup must be dropped: %v", err)
	}
}

// TestRecoverPendingSweepsOrphanedStaging ensures a crashed install's staging
// dir is reclaimed rather than accumulating under the parent.
func TestRecoverPendingSweepsOrphanedStaging(t *testing.T) {
	parent := t.TempDir()
	pluginsDir := filepath.Join(parent, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(parent, stagingPrefix+"deadbeef")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "x"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RecoverPending(pluginsDir); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned staging not swept: %v", err)
	}
}

// TestRecoverPendingIdempotent repeats a recovery to confirm it converges to a
// fixed point instead of thrashing: running it twice on a already-converged
// root leaves everything unchanged.
func TestRecoverPendingIdempotent(t *testing.T) {
	pluginsDir := t.TempDir()
	id := "zero.demo"
	target := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, manifestFileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(filepath.Clean(pluginsDir)), swapPrefix+id)
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := RecoverPending(pluginsDir); err != nil {
			t.Fatalf("RecoverPending pass %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(target, manifestFileName))
	if err != nil {
		t.Fatalf("previous install not restored after repeat: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("restored content = %q, want %q", string(data), "old")
	}
}

// TestInstallRecoversInterruptedPriorReplace drives the full Install path with
// a stranded backup from a previously-killed replace already present, and
// checks the new install still lands cleanly (old restored then replaced),
// with no stranded backup or staging left behind.
func TestInstallRecoversInterruptedPriorReplace(t *testing.T) {
	parent := t.TempDir()
	pluginsDir := filepath.Join(parent, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "zero.demo"
	target := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, manifestFileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Kill mid-replace: old tree stashed, target absent.
	backup := filepath.Join(parent, swapPrefix+id)
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}

	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: pluginsDir}); err != nil {
		t.Fatalf("Install after interrupted prior replace: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, manifestFileName))
	if err != nil {
		t.Fatalf("target missing after recover+install: %v", err)
	}
	// New install lands its own manifest, not the stashed old one's content.
	if strings.Contains(string(data), "\"old\"") {
		t.Fatalf("install wrote the stale old tree back instead of the new one")
	}
	// Nothing stranded in the parent.
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), swapPrefix) || strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Fatalf("stranded backup/staging after recover+install: %s", e.Name())
		}
	}
}

// TestRecoverPendingAlignsWithSwapBackupLocation guards the bug where
// swapStagedPluginIntoPlace stashed the backup in a different directory than
// RecoverPending scanned (backup in the install dir, scan in its parent): a
// crash between the two renames left the canonical install absent with the old
// tree stranded where recovery never looked. It uses the SAME backup path the
// swap writes (swapBackupPath) to set up the interrupted state, then recovers —
// so a drift between the write site and the scan site leaves the backup
// stranded and the canonical install missing, failing here instead of in
// production at crash time.
func TestRecoverPendingAlignsWithSwapBackupLocation(t *testing.T) {
	parent := t.TempDir()
	pluginsDir := filepath.Join(parent, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "zero.demo"
	target := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, manifestFileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(parent, stagingPrefix+"1")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, manifestFileName), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate a kill AFTER the stash but BEFORE the move-in, using the exact
	// backup path swapStagedPluginIntoPlace writes — not a hand-rolled copy of
	// its expression. target is absent; the old tree sits at the backup path.
	backup := swapBackupPath(target)
	if err := os.Rename(target, backup); err != nil {
		t.Fatalf("simulate stash: %v", err)
	}

	if err := RecoverPending(pluginsDir); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, manifestFileName))
	if err != nil {
		t.Fatalf("previous install not restored from real swap backup path: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("restored content = %q, want %q", string(data), "old")
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup stranded at %s where RecoverPending does not scan: %v", backup, err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned staging not swept: %v", err)
	}
}
