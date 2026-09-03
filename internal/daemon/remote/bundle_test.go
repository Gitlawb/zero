package remote

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/lockutil"
)

// initTestRepo creates a temp git work tree with one committed file and returns
// its path. It sets a deterministic identity so it does not depend on global git
// config.
func initTestRepo(t *testing.T, file, content string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.test")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestGitBundleRoundTrip(t *testing.T) {
	repo := initTestRepo(t, "a.txt", "content")
	ctx := context.Background()
	bundle := filepath.Join(t.TempDir(), "x.bundle")
	if err := gitBundleCreate(ctx, repo, bundle); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := gitBundleVerify(ctx, bundle); err != nil {
		t.Fatalf("verify: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "clone")
	if err := gitClone(ctx, bundle, dest); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); err != nil {
		t.Fatalf("cloned tree missing file: %v", err)
	}
}

func TestIsGitWorktree(t *testing.T) {
	repo := initTestRepo(t, "f", "x")
	if !isGitWorktree(repo) {
		t.Fatal("a git repo should be detected")
	}
	if isGitWorktree(t.TempDir()) {
		t.Fatal("a plain dir should not be detected as a git repo")
	}
}

func TestSanitizeLinkID(t *testing.T) {
	for _, ok := range []string{"proj", "proj-1", "a_b.c", "ABC123"} {
		if _, err := sanitizeLinkID(ok); err != nil {
			t.Fatalf("sanitizeLinkID(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "   ", ".", "..", "a/b", "a\\b", "a b", "a$b", string(make([]byte, 200))} {
		if _, err := sanitizeLinkID(bad); err == nil {
			t.Fatalf("sanitizeLinkID(%q) should error", bad)
		}
	}
}

func TestWithinDir(t *testing.T) {
	root := t.TempDir()
	if !withinDir(root, filepath.Join(root, "child")) {
		t.Fatal("child should be within root")
	}
	if withinDir(root, filepath.Dir(root)) {
		t.Fatal("parent should not be within root")
	}
}

func TestSessionLinkSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "link.json")
	link := SessionLink{Address: "host:9000", ServerName: "host", LinkID: "proj-1", RemotePath: "/bundles/proj-1", BundleSHA256: "deadbeef"}
	if err := link.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("link file perm = %v, want 0600", perm)
		}
	}
	got, err := LoadSessionLink(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != link {
		t.Fatalf("roundtrip mismatch: %+v != %+v", *got, link)
	}
}

func TestSessionLinkValidate(t *testing.T) {
	for _, bad := range []SessionLink{
		{LinkID: "p", RemotePath: "/r"},    // no address
		{Address: "h:1", RemotePath: "/r"}, // no link id
		{Address: "h:1", LinkID: "p"},      // no remote path
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("Validate(%+v) should error", bad)
		}
		if err := bad.Save(filepath.Join(t.TempDir(), "x.json")); err == nil {
			t.Fatalf("Save of invalid link %+v should error", bad)
		}
	}
}

func TestUploadRepoBundleRejectsNonRepo(t *testing.T) {
	// A non-git dir is rejected before any dial, so the bogus address is never used.
	_, err := UploadRepoBundle(RemoteConfig{Address: "127.0.0.1:1", Token: "t"}, t.TempDir(), "p")
	if err == nil {
		t.Fatal("a non-git dir should be rejected")
	}
}

func TestBridgeBundleUploadRoundTrip(t *testing.T) {
	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("tok")
	bundleRoot := t.TempDir()
	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, BundleDir: bundleRoot})

	repo := initTestRepo(t, "hello.txt", "hi there")
	link, err := UploadRepoBundle(RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}, repo, "proj-1")
	if err != nil {
		t.Fatalf("UploadRepoBundle: %v", err)
	}
	wantPath := filepath.Join(bundleRoot, "proj-1")
	if link.RemotePath != wantPath {
		t.Fatalf("remote path = %q, want %q", link.RemotePath, wantPath)
	}
	if link.BundleSHA256 == "" {
		t.Fatal("link should carry a bundle sha256")
	}
	// The extracted work tree should contain the committed file.
	data, err := os.ReadFile(filepath.Join(link.RemotePath, "hello.txt"))
	if err != nil {
		t.Fatalf("extracted tree missing file: %v", err)
	}
	if string(data) != "hi there" {
		t.Fatalf("extracted file content = %q", data)
	}

	// A second upload to the same link id replaces the prior extraction.
	if _, err := UploadRepoBundle(RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}, repo, "proj-1"); err != nil {
		t.Fatalf("re-upload: %v", err)
	}
}

func TestBridgeBundleDisabledByDefault(t *testing.T) {
	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("tok")
	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, AuthFailDelay: -1}) // no BundleDir

	repo := initTestRepo(t, "f", "x")
	_, err := UploadRepoBundle(RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}, repo, "p")
	if err == nil {
		t.Fatal("bundle upload must be refused when --bundle-dir is unset")
	}
}

func TestBridgeBundleRejectsBadToken(t *testing.T) {
	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("correct")
	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, BundleDir: t.TempDir(), AuthFailDelay: -1})

	repo := initTestRepo(t, "f", "x")
	_, err := UploadRepoBundle(RemoteConfig{Address: addr, Token: "wrong", CACertFile: ca}, repo, "p")
	if err == nil {
		t.Fatal("bundle upload with a bad token must be refused")
	}
}

// testBundle commits content to a throwaway repo and returns a bundle of it.
func testBundle(t *testing.T, file, content string) string {
	t.Helper()
	repo := initTestRepo(t, file, content)
	out := filepath.Join(t.TempDir(), "b.bundle")
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if err := gitBundleCreate(ctx, repo, out); err != nil {
		t.Fatalf("bundle create: %v", err)
	}
	return out
}

// An extract that cannot clear the live tree must leave that tree alone. The old
// code removed dest before it had anything to publish, so a partial removal --
// here a subdirectory the daemon cannot delete -- destroyed the prior extraction
// and the deferred staging cleanup then deleted the replacement.
func TestExtractBundleKeepsPriorTreeWhenLiveTreeCannotBeCleared(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions")
	}
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}
	locked := filepath.Join(dest, "locked")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	// The publish moves the old tree (locked subdir included) into staging, so
	// restore write permission wherever it ended up or TempDir cleanup fails.
	t.Cleanup(func() {
		_ = filepath.WalkDir(filepath.Dir(dest), func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})

	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, logf); err != nil {
		t.Fatalf("extract over an undeletable subtree: %v", err)
	}
	// The prior tree moved into staging, so the cleanup cannot delete it either.
	// That strands a whole copy of the repo and must not pass silently.
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "staging dir") }) {
		t.Errorf("a staging dir that could not be removed was not reported: %v", logged)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil {
		t.Fatalf("dest holds neither the old tree nor the new one: %v", err)
	}
	if string(got) != "v1" {
		t.Fatalf("a.txt = %q, want the newly published %q", got, "v1")
	}
}

// Every bundle upload is handled in its own goroutine, so two uploads of one
// link id can extract at the same time. The old code let them delete and rename
// over each other: extracts failed with "directory not empty" and one call's
// removal could wipe a tree another had already published.
func TestExtractBundleConcurrentSameDestAlwaysLeavesATree(t *testing.T) {
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "proj-1")
	first := testBundle(t, "a.txt", "v0")
	second := testBundle(t, "a.txt", "v1")
	if err := extractBundle(ctx, first, dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	// The unsafe window is the two back-to-back renames after the clone. Without
	// this delay it is narrow enough that the test still passes a good fraction
	// of the time with the lock removed, which would make it a guard in name only.
	real := stagingFS.rename
	stagingFS.rename = func(from, to string) error {
		time.Sleep(2 * time.Millisecond)
		return real(from, to)
	}
	t.Cleanup(func() { stagingFS.rename = real })

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for round := 0; round < 15; round++ {
		for _, src := range []string{first, second} {
			wg.Add(1)
			go func(src string) {
				defer wg.Done()
				if err := extractBundle(ctx, src, dest, nil); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}(src)
		}
		wg.Wait()
	}

	if len(failures) > 0 {
		t.Errorf("concurrent extracts of one link id failed: %v", failures)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Errorf("dest is not a work tree after concurrent extracts: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil {
		t.Fatalf("dest holds no extraction: %v", err)
	}
	if string(got) != "v0" && string(got) != "v1" {
		t.Fatalf("a.txt = %q, want one of the uploaded trees", got)
	}
}

// Staging directories live beside dest under a reserved dot prefix, so a link id
// must never be able to name one.
func TestSanitizeLinkIDRejectsDotPrefixedIDs(t *testing.T) {
	for _, id := range []string{".", "..", ".staging-1", ".staging-abc", ".git", "..foo", ".hidden"} {
		if _, err := sanitizeLinkID(id); err == nil {
			t.Errorf("sanitizeLinkID(%q) was accepted; it can collide with a staging dir", id)
		}
	}
	for _, id := range []string{"proj-1", "a.b", "x_1", "A1", "repo.git"} {
		if _, err := sanitizeLinkID(id); err != nil {
			t.Errorf("sanitizeLinkID(%q) = %v, want accepted", id, err)
		}
	}
}

// The publish rename is the one step whose failure the old code could not come
// back from: dest was already deleted. It must now put the prior tree back.
func TestExtractBundleRestoresPriorTreeWhenPublishFails(t *testing.T) {
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	// Fail only the publish, so the restore rename that follows it still runs.
	// The clone is what moves in from repo; the set-aside and the restore both
	// have to stay real for the prior tree to come back.
	real := stagingFS.rename
	stagingFS.rename = func(from, to string) error {
		if filepath.Base(from) == "repo" {
			return errors.New("injected publish failure")
		}
		return real(from, to)
	}
	t.Cleanup(func() { stagingFS.rename = real })

	if err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, nil); err == nil {
		t.Fatal("a failed publish must be reported")
	}
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil {
		t.Fatalf("prior extraction was not restored: %v", err)
	}
	if string(got) != "v0" {
		t.Fatalf("a.txt = %q, want the prior %q", got, "v0")
	}
}

// If the publish fails and the prior tree cannot be put back either, dest is
// empty and the backup is the only copy left. It must survive the cleanup so an
// operator can recover it by hand.
func TestExtractBundleKeepsBackupWhenRestoreAlsoFails(t *testing.T) {
	ctx := context.Background()
	bundleDir := t.TempDir()
	dest := filepath.Join(bundleDir, "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	// The publish and the restore are the two renames into dest; the set-aside
	// that fills the backup stays real, or there is no retained copy to assert on.
	real := stagingFS.rename
	stagingFS.rename = func(from, to string) error {
		if to == dest {
			return errors.New("injected rename failure")
		}
		return real(from, to)
	}
	t.Cleanup(func() { stagingFS.rename = real })

	err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, nil)
	if err == nil {
		t.Fatal("a failed publish must be reported")
	}
	if !strings.Contains(err.Error(), "prior tree left in") {
		t.Errorf("error should point at the retained backup, got: %v", err)
	}

	entries, readErr := os.ReadDir(bundleDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	found := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			if _, statErr := os.Stat(filepath.Join(bundleDir, e.Name(), "backup", "a.txt")); statErr == nil {
				found = e.Name()
			}
		}
	}
	if found == "" {
		t.Fatal("the prior tree was deleted along with the staging dir")
	}
	got, readErr := os.ReadFile(filepath.Join(bundleDir, found, "backup", "a.txt"))
	if readErr != nil || string(got) != "v0" {
		t.Fatalf("retained backup = %q, err %v, want %q", got, readErr, "v0")
	}

	// The retained tree carries its link marker, so the next bridge start puts
	// it back rather than leaving the link empty forever.
	stagingFS.rename = real
	recoverBundleDir(bundleDir, nil)
	got, readErr = os.ReadFile(filepath.Join(dest, "a.txt"))
	if readErr != nil {
		t.Fatalf("recovery did not restore the retained tree: %v", readErr)
	}
	if string(got) != "v0" {
		t.Fatalf("recovered a.txt = %q, want %q", got, "v0")
	}
}

// soleStaging returns the one staging dir under dir, and fails if there is not
// exactly one. A test that asserts on "the" staging dir has to prove that is
// what it found, or a second one left by an earlier step reads as a pass.
func soleStaging(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := []string{}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), stagingPrefix) {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	if len(found) != 1 {
		t.Fatalf("staging dirs in %s = %v, want exactly one", dir, found)
	}
	return found[0]
}

// The marker is what lets recovery name the copy a crash leaves behind, so it
// has to be on disk before anything destructive runs. A clone that fails is the
// first step after it, and the marker must already be readable there.
func TestExtractBundleWritesTheMarkerBeforeTheClone(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	// The staging dir is cleaned up on the way out of a failed extract, so the
	// cleanup is blocked to leave the on-disk state this test is about.
	injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))

	err := extractBundle(context.Background(), filepath.Join(dir, "missing.bundle"), dest, nil)
	if err == nil {
		t.Fatal("cloning a bundle that does not exist must fail")
	}

	staging := soleStaging(t, dir)
	seq, ok := stagingStamp(filepath.Base(staging))
	if !ok {
		t.Fatalf("staging name %s carries no sequence", staging)
	}
	m, err := readMarker(staging)
	if err != nil {
		t.Fatalf("read the marker the extract should have written: %v", err)
	}
	if want := (txnMarker{Kind: txnKindBundleExtract, Dest: "proj-1", Seq: seq}); m != want {
		t.Fatalf("marker = %+v, want %+v", m, want)
	}
	if _, err := os.Stat(filepath.Join(staging, "backup")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been set aside before the clone, got %v", err)
	}
}

// A torn marker occupies the name recovery reads and parses as nothing, so the
// bytes reach their name through a rename or not at all.
func TestExtractBundleMarkerIsAtomic(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	injected := errors.New("injected marker rename failure")
	injectFault(t, "rename", func(args ...string) bool { return filepath.Base(args[1]) == stagingMarkerFile }, injected)
	injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))

	err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, nil)
	if !errors.Is(err, injected) {
		t.Fatalf("extract = %v, want the injected marker rename failure", err)
	}

	staging := soleStaging(t, dir)
	entries, readErr := os.ReadDir(staging)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, e := range entries {
		if e.Name() == stagingMarkerFile {
			t.Errorf("a marker that never got renamed must not occupy %s", filepath.Join(staging, stagingMarkerFile))
		}
	}
	got, readErr := os.ReadFile(filepath.Join(dest, "a.txt"))
	if readErr != nil || string(got) != "v0" {
		t.Fatalf("dest a.txt = %q, err %v, want the prior tree %q untouched", got, readErr, "v0")
	}
	if _, err := os.Stat(filepath.Join(staging, "backup")); !os.IsNotExist(err) {
		t.Errorf("a failed marker must stop the extract before the set-aside, got %v", err)
	}
}

// The flag is the evidence a later recovery needs that the copy in backup was
// superseded. It only means that if the publish already landed.
func TestExtractBundleCreatesTheCommitFlagAfterPublish(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	// Blocking the final cleanup is what leaves the committed staging dir on
	// disk to assert on; a cleanup failure is logged, never returned.
	injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))

	if err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, nil); err != nil {
		t.Fatalf("extract = %v, want the cleanup failure to be logged, not returned", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil || string(got) != "v1" {
		t.Fatalf("dest a.txt = %q, err %v, want %q", got, err, "v1")
	}
	staging := soleStaging(t, dir)
	got, err = os.ReadFile(filepath.Join(staging, "backup", "a.txt"))
	if err != nil || string(got) != "v0" {
		t.Fatalf("backup a.txt = %q, err %v, want the prior tree %q", got, err, "v0")
	}
	if _, err := os.Stat(filepath.Join(staging, committedFile)); err != nil {
		t.Fatalf("a published extract must record the commit flag: %v", err)
	}
}

// Without the flag the copy in backup has no proof it was superseded, so the
// staging dir stays: deleting it would drop the only evidence recovery has.
func TestExtractBundleFailedCommitFlagKeepsTheStagingDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	injectFault(t, "create", func(args ...string) bool { return filepath.Base(args[0]) == committedFile }, errors.New("injected commit flag failure"))
	var logged []string
	err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatalf("extract = %v, want a committed publish reported as success", err)
	}

	got, readErr := os.ReadFile(filepath.Join(dest, "a.txt"))
	if readErr != nil || string(got) != "v1" {
		t.Fatalf("dest a.txt = %q, err %v, want the published tree %q", got, readErr, "v1")
	}
	staging := soleStaging(t, dir)
	got, readErr = os.ReadFile(filepath.Join(staging, "backup", "a.txt"))
	if readErr != nil || string(got) != "v0" {
		t.Fatalf("backup a.txt = %q, err %v, want the retained prior tree %q", got, readErr, "v0")
	}
	if _, err := os.Stat(filepath.Join(staging, committedFile)); !os.IsNotExist(err) {
		t.Errorf("the flag failed to be created, so it must not exist: %v", err)
	}
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, staging) }) {
		t.Errorf("the retained staging dir should be named in the log, got %v", logged)
	}
}

// A publish that never landed supersedes nothing, so no flag may appear beside
// the copy that is still the live tree's only other copy.
func TestExtractBundlePublishFailureLeavesNoCommitFlag(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	injected := errors.New("injected publish failure")
	injectFault(t, "rename", func(args ...string) bool { return filepath.Base(args[0]) == "repo" }, injected)
	injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))

	err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, nil)
	if !errors.Is(err, injected) {
		t.Fatalf("extract = %v, want the injected publish failure", err)
	}

	got, readErr := os.ReadFile(filepath.Join(dest, "a.txt"))
	if readErr != nil || string(got) != "v0" {
		t.Fatalf("dest a.txt = %q, err %v, want the restored prior tree %q", got, readErr, "v0")
	}
	staging := soleStaging(t, dir)
	if _, err := os.Stat(filepath.Join(staging, committedFile)); !os.IsNotExist(err) {
		t.Errorf("a failed publish must leave no commit flag, got %v", err)
	}
}

// Every rename goes through fsutil.RenameWithRetry, which absorbs the momentary
// sharing violation an open file produces on Windows. The retry is Windows-only
// (fsutil/rename.go: ten attempts, 10 ms apart, guarded by runtime.GOOS), so on
// every other platform the assertion is only that the error comes back
// unchanged. This guard is falsifiable on Windows CI alone.
func TestExtractBundleRenamesRetryOnTransientErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	if err := extractBundle(ctx, testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	// The third rename of the second extract is the publish: the marker, then
	// the set-aside, then the swap into dest.
	injected := &os.LinkError{Op: "rename", Old: "repo", New: dest, Err: syscall.Errno(32)}
	injectFault(t, "rename", nthCall(3), injected)

	err := extractBundle(ctx, testBundle(t, "a.txt", "v1"), dest, nil)
	want := "v0"
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("extract = %v, want the sharing violation absorbed by the retry", err)
		}
		want = "v1"
	} else if !errors.Is(err, injected) {
		t.Fatalf("extract = %v, want the sharing violation returned unchanged", err)
	}
	got, readErr := os.ReadFile(filepath.Join(dest, "a.txt"))
	if readErr != nil || string(got) != want {
		t.Fatalf("dest a.txt = %q, err %v, want %q", got, readErr, want)
	}
}

// Missing and unreadable are opposite decisions for recovery: the first says the
// directory was never this code's to act on, the second says the filesystem
// failed and nothing may be concluded. Folding one into the other is what turns
// a fault into a licence.
func TestReadMarkerDistinguishesMissingFromUnreadable(t *testing.T) {
	dir := t.TempDir()

	if _, err := readMarker(dir); !errors.Is(err, errMarkerMissing) {
		t.Fatalf("readMarker of a dir with no marker = %v, want errMarkerMissing", err)
	}

	// What a released version left at this name is not JSON; it parses as
	// nothing, which is a marker that exists and cannot be trusted.
	if err := os.WriteFile(filepath.Join(dir, stagingMarkerFile), []byte("proj-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readMarker(dir)
	if err == nil {
		t.Fatal("an unparseable marker must be an error")
	}
	if errors.Is(err, errMarkerMissing) {
		t.Fatalf("an unparseable marker = %v, want an error distinct from errMarkerMissing", err)
	}

	injected := errors.New("injected marker read failure")
	injectFault(t, "readFile", nil, injected)
	_, err = readMarker(dir)
	if !errors.Is(err, injected) {
		t.Fatalf("readMarker over a failing read = %v, want the injected failure", err)
	}
	if errors.Is(err, errMarkerMissing) {
		t.Fatalf("a read failure = %v, want an error distinct from errMarkerMissing", err)
	}
}

// The link marker is read off disk, so a hostile or corrupt one must not steer a
// rename anywhere outside the bundle dir.
func TestRecoverBundleDirRefusesAMarkerThatEscapesTheBundleDir(t *testing.T) {
	for _, marker := range []string{"../evil", "/etc/evil", "..", "a/b", ".hidden"} {
		dir := t.TempDir()
		staging := plantInterruptedExtract(t, dir, "proj-1", "v0")
		if err := writeMarker(staging, txnMarker{Kind: txnKindBundleExtract, Dest: marker}); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(filepath.Dir(dir), "evil")

		var logged []string
		recoverBundleDir(dir, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })

		if _, err := os.Stat(outside); !os.IsNotExist(err) {
			t.Errorf("marker %q created %s: %v", marker, outside, err)
		}
		if _, err := os.Stat(filepath.Join(staging, "backup", "a.txt")); err != nil {
			t.Errorf("marker %q: the tree should be left in place, not moved: %v", marker, err)
		}
		if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "leaving it in place") }) {
			t.Errorf("marker %q should be reported, got %v", marker, logged)
		}
	}
}

// plantInterruptedExtract builds the on-disk state a crash between the two swap
// renames leaves behind: the link's only tree sitting in a staging dir.
func plantInterruptedExtract(t *testing.T, bundleDir, linkID, content string) string {
	t.Helper()
	staging := filepath.Join(bundleDir, stagingPrefix+"crashed")
	backup := filepath.Join(staging, "backup")
	if err := os.MkdirAll(filepath.Join(backup, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(staging, txnMarker{Kind: txnKindBundleExtract, Dest: linkID}); err != nil {
		t.Fatal(err)
	}
	return staging
}

func TestRecoverBundleDirRestoresInterruptedExtract(t *testing.T) {
	dir := t.TempDir()
	staging := plantInterruptedExtract(t, dir, "proj-1", "v0")

	var logged []string
	recoverBundleDir(dir, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })

	got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt"))
	if err != nil {
		t.Fatalf("the interrupted extract was not restored: %v", err)
	}
	if string(got) != "v0" {
		t.Fatalf("restored a.txt = %q, want %q", got, "v0")
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staging should be gone after a successful restore, got %v", err)
	}
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "restored the work tree") }) {
		t.Errorf("a restore should be reported, got %v", logged)
	}
}

func TestRecoverBundleDirDropsBackupWhenTheLinkAlreadyHasATree(t *testing.T) {
	dir := t.TempDir()
	staging := plantInterruptedExtract(t, dir, "proj-1", "stale")
	live := filepath.Join(dir, "proj-1")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "a.txt"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}

	recoverBundleDir(dir, nil)

	got, err := os.ReadFile(filepath.Join(live, "a.txt"))
	if err != nil || string(got) != "live" {
		t.Fatalf("the live tree must win: got %q err %v", got, err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("a superseded backup should be removed, got %v", err)
	}
}

func TestRecoverBundleDirKeepsABackupItCannotAttribute(t *testing.T) {
	dir := t.TempDir()
	staging := plantInterruptedExtract(t, dir, "proj-1", "v0")
	if err := os.Remove(filepath.Join(staging, stagingMarkerFile)); err != nil {
		t.Fatal(err)
	}

	var logged []string
	recoverBundleDir(dir, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })

	if _, err := os.Stat(filepath.Join(staging, "backup", "a.txt")); err != nil {
		t.Errorf("an unattributable tree must be left alone, not deleted: %v", err)
	}
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "no usable transaction marker") }) {
		t.Errorf("an unattributable tree should be reported, got %v", logged)
	}
}

func TestRecoverBundleDirLeavesAStagingDirAnExtractCouldStillOwn(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, stagingPrefix+"live")
	if err := os.MkdirAll(filepath.Join(staging, "repo"), 0o700); err != nil {
		t.Fatal(err)
	}

	recoverBundleDir(dir, nil)

	if _, err := os.Stat(staging); err != nil {
		t.Errorf("a fresh staging dir may belong to a running clone: %v", err)
	}
}

func TestRecoverBundleDirReapsAbandonedStaging(t *testing.T) {
	// Both name shapes: the stamped one is what extractBundle writes now, the
	// bare one is any staging dir whose name the reaper cannot read.
	for _, name := range []string{"old", "00000000000000000100-old"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			staging := filepath.Join(dir, stagingPrefix+name)
			if err := os.MkdirAll(filepath.Join(staging, "repo"), 0o700); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-3 * gitTimeout)
			if err := os.Chtimes(staging, old, old); err != nil {
				t.Fatal(err)
			}

			recoverBundleDir(dir, nil)

			if _, err := os.Stat(staging); !os.IsNotExist(err) {
				t.Errorf("a staging dir older than any clone should be reaped, got %v", err)
			}
		})
	}
}

// A second daemon sharing the bundle dir must not extract the same link at the
// same time; the in-process lock cannot see it, so an advisory file lock does.
func TestExtractBundleWaitsForACrossProcessLock(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	b := testBundle(t, "a.txt", "v0")

	lockDir := filepath.Join(dir, lockDirName)
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lockutil.TryAcquireFileLockAt(dir, filepath.Join(lockDir, "proj-1.lock"))
	if err != nil {
		t.Fatalf("take the lock as the other daemon would: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := extractBundle(ctx, b, dest, nil); err == nil {
		t.Fatal("an extract must not proceed while another process holds the link")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("a blocked extract must not touch dest, got %v", err)
	}

	// Once the other holder is gone the same extract goes through.
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	if err := extractBundle(context.Background(), b, dest, nil); err != nil {
		t.Fatalf("extract after the lock was released: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); err != nil {
		t.Errorf("extract did not publish: %v", err)
	}
}

// End to end over TLS: two clients uploading the same link id at once must both
// succeed and leave one valid work tree.
func TestBridgeConcurrentUploadsOfOneLinkID(t *testing.T) {
	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("tok")
	bundleRoot := t.TempDir()
	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, BundleDir: bundleRoot})

	first := initTestRepo(t, "a.txt", "v0")
	second := initTestRepo(t, "a.txt", "v1")
	cfg := RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for _, repo := range []string{first, second, first, second} {
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			if _, err := UploadRepoBundle(cfg, repo, "proj-1"); err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}(repo)
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Errorf("concurrent uploads of one link id failed: %v", failures)
	}
	dest := filepath.Join(bundleRoot, "proj-1")
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("the link has no work tree after concurrent uploads: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil {
		t.Fatalf("extracted tree is missing its file: %v", err)
	}
	if string(got) != "v0" && string(got) != "v1" {
		t.Fatalf("a.txt = %q, want one of the uploaded trees", got)
	}
}

// End to end: a link id that could name a staging dir is refused, and refused
// before the client dials.
func TestBridgeRejectsDotPrefixedLinkID(t *testing.T) {
	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("tok")
	bundleRoot := t.TempDir()
	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, BundleDir: bundleRoot})

	repo := initTestRepo(t, "a.txt", "v0")
	for _, id := range []string{".staging-1", ".git", ".hidden"} {
		if _, err := UploadRepoBundle(RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}, repo, id); err == nil {
			t.Errorf("upload with link id %q should be refused", id)
		}
		if _, err := os.Stat(filepath.Join(bundleRoot, id)); !os.IsNotExist(err) {
			t.Errorf("link id %q must not create %s: %v", id, filepath.Join(bundleRoot, id), err)
		}
	}
}

// End to end: a bridge started over a bundle dir a crash left mid-swap puts the
// tree back before it serves anything, and the link is usable again.
func TestBridgeRecoversInterruptedExtractOnStart(t *testing.T) {
	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("tok")
	bundleRoot := t.TempDir()
	staging := plantInterruptedExtract(t, bundleRoot, "proj-1", "recovered")

	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, BundleDir: bundleRoot})

	got, err := os.ReadFile(filepath.Join(bundleRoot, "proj-1", "a.txt"))
	if err != nil {
		t.Fatalf("the bridge did not restore the interrupted extract: %v", err)
	}
	if string(got) != "recovered" {
		t.Fatalf("restored a.txt = %q, want %q", got, "recovered")
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staging should be cleared after recovery, got %v", err)
	}
	// The recovered link still accepts a fresh upload.
	repo := initTestRepo(t, "a.txt", "v2")
	if _, err := UploadRepoBundle(RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}, repo, "proj-1"); err != nil {
		t.Fatalf("upload to a recovered link: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(bundleRoot, "proj-1", "a.txt"))
	if err != nil || string(got) != "v2" {
		t.Fatalf("after re-upload a.txt = %q, err %v, want %q", got, err, "v2")
	}
}

// A crashed extract and a live one look identical on disk: the backup is aside
// and dest is briefly absent. A second daemon starting in that window must not
// take the running extract's tree.
func TestRecoverBundleDirLeavesALiveExtractAlone(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	if err := extractBundle(context.Background(), testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
		t.Fatalf("seed extract: %v", err)
	}

	inPublish := make(chan struct{})
	real := stagingFS.rename
	var once sync.Once
	stagingFS.rename = func(from, to string) error {
		// Stall only the publish (clone into dest), not the restore.
		if filepath.Base(from) == "repo" {
			once.Do(func() { close(inPublish) })
			time.Sleep(300 * time.Millisecond)
		}
		return real(from, to)
	}
	t.Cleanup(func() { stagingFS.rename = real })

	var wg sync.WaitGroup
	var extractErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		extractErr = extractBundle(context.Background(), testBundle(t, "a.txt", "v1"), dest, nil)
	}()

	<-inPublish
	time.Sleep(20 * time.Millisecond) // the second daemon starts mid-swap
	recoverBundleDir(dir, nil)
	wg.Wait()

	if extractErr != nil {
		t.Errorf("recovery interfered with a live extract: %v", extractErr)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil {
		t.Fatalf("dest holds no tree after the live extract: %v", err)
	}
	if string(got) != "v1" {
		t.Fatalf("a.txt = %q, want the published %q", got, "v1")
	}
}

// stageBackup plants a staging dir holding a backup tree for linkID, named the
// way extractBundle names one so recovery can order it. A stamp above 0 gets the
// allocator's exact grammar, because that is now the only shape recovery orders;
// the name argument then only distinguishes unstamped fixtures, which are the
// shape recovery must refuse to order. Two stamped fixtures in one directory
// need distinct stamps, as two extracts do.
func stageBackup(t *testing.T, dir, name, linkID, content string, stamp int64) string {
	t.Helper()
	if stamp > 0 {
		name = fmt.Sprintf("%020d%s", stamp, stagingSeqSuffix)
	}
	staging := filepath.Join(dir, stagingPrefix+name)
	backup := filepath.Join(staging, "backup")
	if err := os.MkdirAll(filepath.Join(backup, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	seq, _ := stagingStamp(filepath.Base(staging))
	if err := writeMarker(staging, txnMarker{Kind: txnKindBundleExtract, Dest: linkID, Seq: seq}); err != nil {
		t.Fatal(err)
	}
	return staging
}

// One link can end up with more than one staged backup: a staging cleanup that
// could not finish leaves one behind, and a later crash adds another. Recovery
// must bring back the newest, and must not delete the newer one as superseded
// just because directory order put the older one first.
func TestRecoverBundleDirRestoresTheNewestOfSeveralBackups(t *testing.T) {
	dir := t.TempDir()
	// Lexically first, so directory order alone would pick the older tree.
	stageBackup(t, dir, "aaa-old", "proj-1", "v0", 100)
	newer := stageBackup(t, dir, "zzz-new", "proj-1", "v1", 200)

	recoverBundleDir(dir, nil)

	got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt"))
	if err != nil {
		t.Fatalf("nothing was restored: %v", err)
	}
	if string(got) != "v1" {
		t.Errorf("restored a.txt = %q, want the newest tree %q", got, "v1")
		if _, err := os.Stat(filepath.Join(newer, "backup", "a.txt")); err != nil {
			t.Errorf("and the newest tree was deleted as superseded: %v", err)
		}
	}
}

// Dropping a backup rests on the live tree having been published over it, which
// is not true of a tree recovery itself just put back. A second backup that
// carries no order recovery can compare against that restore may be the newer
// copy, so it is kept rather than dropped.
// parkedStaging is where recovery moves a staging dir it kept, so a later pass
// does not read the restored tree at dest as a later extract publishing over it.
func parkedStaging(staging string) string {
	base := filepath.Base(staging)
	return filepath.Join(filepath.Dir(staging), keptPrefix+strings.TrimPrefix(base, stagingPrefix))
}

func TestRecoverBundleDirKeepsAnUnorderableBackupAgainstATreeItRestored(t *testing.T) {
	dir := t.TempDir()
	stamped := stageBackup(t, dir, "current", "proj-1", "v1", 200)
	unordered := stageBackup(t, dir, "leftover", "proj-1", "v2", 0)

	var logged []string
	recoverBundleDir(dir, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })

	got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt"))
	if err != nil || string(got) != "v1" {
		t.Fatalf("the stamped backup should be restored: got %q err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(stamped, "backup")); !os.IsNotExist(err) {
		t.Errorf("the restored staging dir should be cleared, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(parkedStaging(unordered), "backup", "a.txt")); err != nil {
		t.Errorf("a backup that cannot be ordered against the restore must be kept: %v", err)
	}
	if _, err := os.Stat(unordered); !os.IsNotExist(err) {
		t.Errorf("the kept backup should be parked out of the scanned prefix, got %v", err)
	}
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "cannot be ordered") }) {
		t.Errorf("keeping an unorderable backup should be reported, got %v", logged)
	}
}

// Recovery runs on every start. A backup the pass above deliberately kept must
// still be there after the next one, where the restore is already at dest and
// nothing records that this pass, not a later extract, put it there.
func TestRecoverBundleDirKeepsAnUnorderableBackupAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	stageBackup(t, dir, "current", "proj-1", "v1", 200)
	unordered := stageBackup(t, dir, "leftover", "proj-1", "v2", 0)

	recoverBundleDir(dir, nil)
	if _, err := os.Stat(filepath.Join(parkedStaging(unordered), "backup", "a.txt")); err != nil {
		t.Fatalf("the first pass should keep the unorderable backup: %v", err)
	}

	// The daemon restarts: same dir, a fresh recovery pass with no memory of the
	// first.
	recoverBundleDir(dir, nil)

	if !keptBackupSurvives(t, dir, "v2") {
		t.Error("a backup kept as unorderable must survive the next recovery pass")
	}
	got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt"))
	if err != nil || string(got) != "v1" {
		t.Errorf("the restored tree must be left alone across restarts: got %q err %v", got, err)
	}

	// And it must not be re-restored or re-parked on every start after that.
	recoverBundleDir(dir, nil)
	if !keptBackupSurvives(t, dir, "v2") {
		t.Error("the kept backup must survive a third pass too")
	}
}

// keptBackupSurvives reports whether a backup holding content is still somewhere
// under dir, wherever recovery decided to keep it.
func keptBackupSurvives(t *testing.T, dir, content string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		got, err := os.ReadFile(filepath.Join(dir, entry.Name(), "backup", "a.txt"))
		if err == nil && string(got) == content {
			return true
		}
	}
	return false
}

// Parking is a rename, and a rename onto a directory that already holds
// something must fail rather than replace it. Recovery then leaves the copy
// where it is, which is still a copy, and never eats the occupant.
func TestRecoverBundleDirDoesNotClobberAnOccupiedParkedName(t *testing.T) {
	dir := t.TempDir()
	stageBackup(t, dir, "current", "proj-1", "v1", 200)
	unordered := stageBackup(t, dir, "leftover", "proj-1", "v2", 0)

	occupied := parkedStaging(unordered)
	if err := os.MkdirAll(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "keep.txt"), []byte("not mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	var logged []string
	recoverBundleDir(dir, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })

	got, err := os.ReadFile(filepath.Join(occupied, "keep.txt"))
	if err != nil || string(got) != "not mine" {
		t.Errorf("the occupant of the parked name must be untouched: got %q err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(unordered, "backup", "a.txt")); err != nil {
		t.Errorf("a backup that could not be parked must stay where it is: %v", err)
	}
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "could not park") }) {
		t.Errorf("a failed park should be reported, got %v", logged)
	}
}

// An older backup IS dropped once the tree restored over it is provably newer,
// so the fail-safe above does not turn into a leak of every leftover.
func TestRecoverBundleDirDropsAnOlderBackupAfterRestoringANewerOne(t *testing.T) {
	dir := t.TempDir()
	stale := stageBackup(t, dir, "stale", "proj-1", "v0", 100)
	stageBackup(t, dir, "current", "proj-1", "v1", 200)

	recoverBundleDir(dir, nil)

	got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt"))
	if err != nil || string(got) != "v1" {
		t.Fatalf("the newest backup should be restored: got %q err %v", got, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the superseded backup should be removed, got %v", err)
	}
}

// Link ids starting with '.' used to be accepted, so a work tree may already be
// published under a name that now looks like a staging dir. The reaper must not
// mistake it for an abandoned extract and delete it on the first start.
func TestRecoverBundleDirKeepsAWorkTreePublishedUnderAReservedName(t *testing.T) {
	dir := t.TempDir()
	published := filepath.Join(dir, stagingPrefix+"foo")
	if err := os.MkdirAll(filepath.Join(published, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(published, "a.txt"), []byte("someones repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * gitTimeout)
	if err := os.Chtimes(published, old, old); err != nil {
		t.Fatal(err)
	}

	recoverBundleDir(dir, nil)

	if _, err := os.Stat(filepath.Join(published, "a.txt")); err != nil {
		t.Errorf("a published work tree was reaped on upgrade: %v", err)
	}
}

// A coarse-resolution filesystem can stamp the backup and the live tree with the
// same directory mtime, which is why recovery must not decide staleness from
// timestamps at all: a backup only ever holds the tree that was live BEFORE the
// one at dest, so a link that has a tree supersedes it either way.
func TestRecoverBundleDirDropsBackupWhenMtimesAreEqual(t *testing.T) {
	dir := t.TempDir()
	staging := plantInterruptedExtract(t, dir, "proj-1", "stale")
	live := filepath.Join(dir, "proj-1")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "a.txt"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	// What a one-second-granularity filesystem produces for a backup and a
	// publish that happen in the same second.
	tie := time.Unix(1700000000, 0)
	for _, path := range []string{filepath.Join(staging, "backup"), live} {
		if err := os.Chtimes(path, tie, tie); err != nil {
			t.Fatal(err)
		}
	}

	recoverBundleDir(dir, nil)

	got, err := os.ReadFile(filepath.Join(live, "a.txt"))
	if err != nil || string(got) != "live" {
		t.Fatalf("the live tree must win: got %q err %v", got, err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("a superseded backup should be removed, got %v", err)
	}
}

// End to end over the real transaction. A real upload publishes a tree, a second
// upload is interrupted exactly the way a killed daemon interrupts it (the
// publish rename and the restore behind it both fail, so the prior tree is left
// in staging), and a fresh bridge on the same directory puts it back. Nothing
// here plants a fixture: the staging name recovery has to order by is the one
// extractBundle writes, which is the half a hand-built fixture cannot check.
func TestBridgeRecoversARealInterruptedExtractOnStart(t *testing.T) {
	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("tok")
	bundleRoot := t.TempDir()
	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, BundleDir: bundleRoot})
	cfg := RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}
	dest := filepath.Join(bundleRoot, "proj-1")

	if _, err := UploadRepoBundle(cfg, initTestRepo(t, "a.txt", "v1"), "proj-1"); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(got) != "v1" {
		t.Fatalf("first upload did not publish: got %q err %v", got, err)
	}

	// Both renames fail, which is the one path that leaves the tree in staging
	// with dest absent -- what a process killed between the two renames leaves.
	real := stagingFS.rename
	stagingFS.rename = func(from, to string) error {
		if to == dest {
			return errors.New("injected rename failure")
		}
		return real(from, to)
	}
	_, err := UploadRepoBundle(cfg, initTestRepo(t, "a.txt", "v2"), "proj-1")
	stagingFS.rename = real
	if err == nil {
		t.Fatal("an upload whose publish and restore both fail must report an error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("the interrupted swap should leave dest absent, got %v", err)
	}

	// The name extractBundle wrote must be one recovery can order by. If the
	// writer and the reader ever disagree on the format, this is where it shows.
	entries, err := os.ReadDir(bundleRoot)
	if err != nil {
		t.Fatal(err)
	}
	stagedNames := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			stagedNames = append(stagedNames, e.Name())
		}
	}
	if len(stagedNames) != 1 {
		t.Fatalf("want exactly one staging dir left behind, got %v", stagedNames)
	}
	if _, ok := stagingStamp(stagedNames[0]); !ok {
		t.Fatalf("recovery cannot order the name extractBundle wrote: %q", stagedNames[0])
	}

	// A fresh daemon over the same directory repairs it before serving.
	addr2, ca2 := startBridge(t, newBridgeServer(t, staticLauncher()), BridgeOptions{Authenticator: auth, BundleDir: bundleRoot})

	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil || string(got) != "v1" {
		t.Fatalf("the interrupted extract was not restored: got %q err %v", got, err)
	}
	for _, name := range stagedNames {
		if _, err := os.Stat(filepath.Join(bundleRoot, name)); !os.IsNotExist(err) {
			t.Errorf("staging %s should be cleared after recovery, got %v", name, err)
		}
	}
	// And the repaired link still serves.
	if _, err := UploadRepoBundle(RemoteConfig{Address: addr2, Token: "tok", CACertFile: ca2}, initTestRepo(t, "a.txt", "v3"), "proj-1"); err != nil {
		t.Fatalf("upload to a recovered link: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(got) != "v3" {
		t.Fatalf("after re-upload a.txt = %q, err %v, want %q", got, err, "v3")
	}
}

// A backward clock leaves an OLDER backup carrying a LARGER stamp. Recovery must
// still restore the tree that was actually live last, and must not delete it as
// superseded on the way.
//
// Fixture order is load-bearing: NewBridge repairs the directory before it
// serves, so a backup planted before the bridge starts is consumed by that first
// pass and this test would pass without proving anything. Publish first, plant
// second.
func TestExtractBundleOutOrdersALeftoverStampedInTheFuture(t *testing.T) {
	// A nanosecond stamp decades ahead, which is what a clock correction
	// backward leaves behind on the earlier of two writes.
	const farFuture = int64(4_000_000_000_000_000_000)

	srv := newBridgeServer(t, staticLauncher())
	auth, _ := NewTokenAuthenticator("tok")
	bundleRoot := t.TempDir()
	addr, ca := startBridge(t, srv, BridgeOptions{Authenticator: auth, BundleDir: bundleRoot})
	cfg := RemoteConfig{Address: addr, Token: "tok", CACertFile: ca}
	dest := filepath.Join(bundleRoot, "proj-1")

	if _, err := UploadRepoBundle(cfg, initTestRepo(t, "a.txt", "v1"), "proj-1"); err != nil {
		t.Fatalf("first upload: %v", err)
	}

	// Only now, with the bridge already up and its recovery pass behind us.
	stale := stageBackup(t, bundleRoot, "old", "proj-1", "v-old", farFuture)

	real := stagingFS.rename
	stagingFS.rename = func(from, to string) error {
		if to == dest {
			return errors.New("injected rename failure")
		}
		return real(from, to)
	}
	_, err := UploadRepoBundle(cfg, initTestRepo(t, "a.txt", "v2"), "proj-1")
	stagingFS.rename = real
	if err == nil {
		t.Fatal("an upload whose publish and restore both fail must report an error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("the interrupted swap should leave dest absent, got %v", err)
	}

	recoverBundleDir(bundleRoot, nil)

	// The interrupted extract set the LIVE tree aside, so v1 is what recovery
	// owes back. The interrupted clone of v2 never reached dest.
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil || string(got) != "v1" {
		t.Errorf("recovery restored %q (err %v), want the tree that was live last, %q", got, err, "v1")
		// Distinguish "restored the wrong one" from "restored the wrong one AND
		// destroyed the right one", which is the data loss this test exists for.
		if !liveTreeSurvivesSomewhere(t, bundleRoot, "v1") {
			t.Error("the tree that was live last was deleted as superseded; no copy of it is left on disk")
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the genuinely older backup should be superseded once the newer one is restored, got %v", err)
	}
}

// The allocator and the parser must agree, and a number already spoken for by a
// kept backup must never come round again.
func TestStagingNamesAllocateInOrderAndParse(t *testing.T) {
	t.Run("counts up and parses", func(t *testing.T) {
		dir := t.TempDir()
		for want := int64(1); want <= 2; want++ {
			seq, err := nextStagingSeq(dir)
			if err != nil {
				t.Fatal(err)
			}
			path, err := createSequencedStagingDir(dir, seq)
			if err != nil {
				t.Fatal(err)
			}
			stamp, ok := stagingStamp(filepath.Base(path))
			if !ok {
				t.Fatalf("the allocator wrote a name recovery cannot order: %q", filepath.Base(path))
			}
			if stamp != want {
				t.Errorf("stamp = %d, want %d", stamp, want)
			}
		}
	})

	// The upgrade case. v0.8.0 staged under os.MkdirTemp(parent, ".staging-*"),
	// whose suffix is a decimal uint32 with no second '-', and this branch's
	// intermediate commits wrote a random one. Neither shape is a sequence this
	// package ever handed out, so the allocator reads none of them and starts at
	// one; the residue is left where it is for recovery to report.
	t.Run("ignores a legacy MkdirTemp-shaped name", func(t *testing.T) {
		dir := t.TempDir()
		const legacy = int64(1_700_000_000_000_000_000)
		name := fmt.Sprintf("%s%020d-x7Kq3", stagingPrefix, legacy)
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		seq, err := nextStagingSeq(dir)
		if err != nil {
			t.Fatal(err)
		}
		if seq != 1 {
			t.Errorf("next sequence = %d, want 1: a name this package never wrote is not a sequence", seq)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("the legacy entry must be left alone: %v", err)
		}
	})

	// A parked backup owns its number for as long as it exists.
	t.Run("skips a number already parked", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("%s%020d%s", keptPrefix, 7, stagingSeqSuffix)), 0o700); err != nil {
			t.Fatal(err)
		}
		seq, err := nextStagingSeq(dir)
		if err != nil {
			t.Fatal(err)
		}
		if seq <= 7 {
			t.Errorf("next sequence = %d, want strictly greater than the parked 7", seq)
		}
	})
}

// The grammar is the first ownership filter: a name that is not exactly what
// createSequencedStagingDir writes was written by something else, and reading a
// sequence out of it is how a legacy work tree gets a say in the ordering.
func TestStagingStampRequiresTheExactGrammar(t *testing.T) {
	// No '*', '?' or ':' in any name here: a table entry Windows cannot name
	// would fail its own setup rather than test the parser.
	cases := []struct {
		name string
		seq  int64
		ok   bool
	}{
		{stagingPrefix + "00000000000000000042" + stagingSeqSuffix, 42, true},
		{stagingPrefix + "42" + stagingSeqSuffix, 0, false},
		// The shape this branch's intermediate commits wrote before the suffix
		// was fixed, and the shape os.MkdirTemp writes.
		{stagingPrefix + "00000000000000000042-x7Kq3", 0, false},
		// The v0.8.0 shape: MkdirTemp's decimal suffix with no second '-'.
		{stagingPrefix + "1234567890", 0, false},
		{stagingPrefix + "00000000000000000042" + stagingSeqSuffix + "-extra", 0, false},
		{stagingPrefix + "0000000000000000004a" + stagingSeqSuffix, 0, false},
		// Twenty characters, and ParseInt would take the sign. A link id may
		// contain '-', so this is a name a legacy work tree can carry.
		{stagingPrefix + "-0000000000000000042" + stagingSeqSuffix, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seq, ok := stagingStamp(tc.name)
			if ok != tc.ok {
				t.Fatalf("stagingStamp(%q) ok = %v, want %v", tc.name, ok, tc.ok)
			}
			if ok && seq != tc.seq {
				t.Errorf("stagingStamp(%q) = %d, want %d", tc.name, seq, tc.seq)
			}
		})
	}
}

// Link ids starting with '.' used to be accepted, so a published work tree can
// sit under a name that now reads as a staging sequence. One at the maximum
// would make the allocator refuse to allocate anything, permanently, and no
// extract for any link in the directory could run again.
func TestNextStagingSeqIgnoresALegacyWorkTreeAtTheMaximum(t *testing.T) {
	dir := t.TempDir()
	// Twenty digits, so it passes the grammar and only the .git veto keeps it
	// out of the ordering.
	planted := []string{
		fmt.Sprintf("%s%020d%s", stagingPrefix, int64(math.MaxInt64), stagingSeqSuffix),
		// Nineteen digits, the name from the review: the grammar alone rejects it.
		stagingPrefix + "9223372036854775807" + stagingSeqSuffix,
	}
	for _, name := range planted {
		if err := os.MkdirAll(filepath.Join(dir, name, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A legacy staging dir at the maximum with no work tree in it: the grammar
	// is the only thing that can keep this one out, so loosening the grammar is
	// visible here even with the veto in place.
	if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("%s%020d-x7Kq3", stagingPrefix, int64(math.MaxInt64))), 0o700); err != nil {
		t.Fatal(err)
	}

	seq, err := nextStagingSeq(dir)
	if err != nil {
		t.Fatalf("a legacy entry must not stop the allocator: %v", err)
	}
	if seq != 1 {
		t.Errorf("next sequence = %d, want 1: none of these names is one this package wrote", seq)
	}
	for _, name := range planted {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("the allocator must not touch %s: %v", name, err)
		}
	}
}

// The veto is keyed on .git at the entry's own root. A live extract's staging
// dir holds its clone one level down, in repo/, so its number still counts;
// handing it out again would put two transactions on one name.
func TestNextStagingSeqCountsAStagingDirHoldingAClone(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, fmt.Sprintf("%s%020d%s", stagingPrefix, 4, stagingSeqSuffix))
	if err := os.MkdirAll(filepath.Join(staging, "repo", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "backup", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	seq, err := nextStagingSeq(dir)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 5 {
		t.Errorf("next sequence = %d, want 5: a live extract's number is still spoken for", seq)
	}
}

// Kept backups are named off the staging name, so they are counted, and they are
// counted under the same grammar: a loose read of one is the same defect as a
// loose read of a staging name, one prefix over.
func TestNextStagingSeqCountsKeptNamesUnderTheSameGrammar(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, fmt.Sprintf("%s%020d%s", keptPrefix, 7, stagingSeqSuffix))
	if err := os.Mkdir(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(kept, txnMarker{Kind: txnKindBundleExtract, Dest: "proj-1", Seq: 7}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("%s%020d-x", keptPrefix, 99)), 0o700); err != nil {
		t.Fatal(err)
	}

	seq, err := nextStagingSeq(dir)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 8 {
		t.Errorf("next sequence = %d, want 8: the parked 7 is spoken for and the loose 99 is not a sequence", seq)
	}
}

// A publish that succeeded but could not clear its staging dir reports success to
// the client while a whole copy of the prior tree stays on disk. The next
// recovery pass reclaims it, and must say so: with no bridge logger configured
// nothing else ever mentions the space that was being held.
func TestRecoverBundleDirReportsReclaimingASupersededTree(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "proj-1")
	// The publish landed, so dest holds the new tree.
	if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The cleanup did not, so the old tree is still staged beside it.
	staging := stageBackup(t, dir, "leftover", "proj-1", "v-old", 100)

	var logged []string
	recoverBundleDir(dir, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })

	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("a staged tree the live one superseded should be reclaimed, got %v", err)
	}
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, staging) }) {
		t.Errorf("reclaiming a superseded staged tree should name it, got %v", logged)
	}
}

// A bundle dir's ordinary contents are the per-link work trees, and a link id is
// whatever the uploading client sent. The allocator must read only the names it
// owns: a link id is not a sequence, however much it looks like one.
func TestStagingSeqIgnoresNamesItDoesNotOwn(t *testing.T) {
	// sanitizeLinkID permits digits, '-' and letters, so every id here is one a
	// client can actually upload under.
	for _, id := range []string{"12345-abc", "2024-project", "09223372036854775807-seq"} {
		t.Run(id, func(t *testing.T) {
			if _, err := sanitizeLinkID(id); err != nil {
				t.Fatalf("fixture is not a link id a client could send: %v", err)
			}
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, id), 0o700); err != nil {
				t.Fatal(err)
			}

			seq, err := nextStagingSeq(dir)
			if err != nil {
				t.Fatalf("a link work tree must not stop the allocator: %v", err)
			}
			if seq != 1 {
				t.Errorf("next sequence = %d, want 1: a link name is not a staging name", seq)
			}
		})
	}
}

// The retry branch: another writer already holds the number this one started
// from, so it walks up rather than failing or reusing.
func TestCreateSequencedStagingDirSkipsAnOccupiedNumber(t *testing.T) {
	dir := t.TempDir()
	taken := fmt.Sprintf("%s%020d%s", stagingPrefix, 1, stagingSeqSuffix)
	if err := os.Mkdir(filepath.Join(dir, taken), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := createSequencedStagingDir(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, fmt.Sprintf("%s%020d%s", stagingPrefix, 2, stagingSeqSuffix))
	if got != want {
		t.Errorf("claimed %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, taken)); err != nil {
		t.Errorf("the occupied name must be left alone: %v", err)
	}
}

// A name at the maximum would make the seeding addition wrap negative, and a
// negative renders a '-' the parser reads as empty digits, so the entry would
// leave the ordering silently. Refusing is the loud version, and the refusal
// belongs where the addition is.
func TestNextStagingSeqRefusesOverflow(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("%s%020d%s", stagingPrefix, int64(math.MaxInt64), stagingSeqSuffix)), 0o700); err != nil {
		t.Fatal(err)
	}
	if seq, err := nextStagingSeq(dir); err == nil {
		t.Errorf("nextStagingSeq returned %d, want an error rather than a wrapped value", seq)
	}
}

func TestCreateSequencedStagingDirRefusesOutOfRange(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int64{0, -1, math.MinInt64} {
		if _, err := createSequencedStagingDir(dir, n); err == nil {
			t.Errorf("createSequencedStagingDir(%d) succeeded, want an error", n)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused allocation must create nothing, got %v", entries)
	}
}

// Exclusive creation is the whole concurrency story, so it gets executed rather
// than argued: no two allocators may come away with the same name or number.
func TestStagingSeqAllocatesDistinctValuesConcurrently(t *testing.T) {
	dir := t.TempDir()
	// 128, not a smaller number: against a check-then-create allocator this
	// detects the duplicate in every run, where 16 caught it about half the time.
	const writers = 128

	var wg sync.WaitGroup
	paths := make([]string, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seq, err := nextStagingSeq(dir)
			if err != nil {
				errs[i] = err
				return
			}
			paths[i], errs[i] = createSequencedStagingDir(dir, seq)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	stamps := map[int64]bool{}
	for i, path := range paths {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v", i, errs[i])
		}
		if seen[path] {
			t.Errorf("two writers claimed the same name %q", path)
		}
		seen[path] = true
		stamp, ok := stagingStamp(filepath.Base(path))
		if !ok {
			t.Errorf("writer %d wrote an unorderable name %q", i, filepath.Base(path))
			continue
		}
		if stamps[stamp] {
			t.Errorf("two writers claimed sequence %d", stamp)
		}
		stamps[stamp] = true
	}
}

// The staging dir holds the only copy of a link's tree mid-swap, so it keeps the
// owner-only mode it has always had. os.Mkdir takes
// a mode where os.MkdirTemp did not, so the allocator has to name it.
func TestStagingDirKeepsOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go permission bits do not map to Windows ACLs")
	}
	dir := t.TempDir()
	path, err := createSequencedStagingDir(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	requireUmaskAllowsWiderThan0700(t, dir)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("staging dir mode = %o, want 0700", got)
	}
}

// requireUmaskAllowsWiderThan0700 skips when the process umask would strip the
// group and other bits anyway. Without it this assertion is vacuous under a
// umask of 077: a wrongly widened 0o755 comes back as 0700 and passes, so the
// one test guarding the mode would silently stop guarding it.
func requireUmaskAllowsWiderThan0700(t *testing.T, parent string) {
	t.Helper()
	control := filepath.Join(parent, ".umask-control")
	if err := os.Mkdir(control, 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(control) }()
	info, err := os.Stat(control)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Skipf("umask masks a 0755 request down to %o, so this assertion cannot tell 0700 from a widened mode", info.Mode().Perm())
	}
}

// liveTreeSurvivesSomewhere reports whether any staging or kept dir under root
// still holds a backup with the given content.
func liveTreeSurvivesSomewhere(t *testing.T, root, content string) bool {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		got, err := os.ReadFile(filepath.Join(root, entry.Name(), "backup", "a.txt"))
		if err == nil && string(got) == content {
			return true
		}
	}
	return false
}

// Recovery decides each link on its own: restoring one link's tree must not make
// another link's superseded backup look unorderable, and vice versa.
func TestRecoverBundleDirHandlesLinksIndependently(t *testing.T) {
	dir := t.TempDir()
	// proj-1 has no tree, so its backup is restored.
	restorable := stageBackup(t, dir, "one", "proj-1", "v1", 100)
	// proj-2 has a live tree, so its backup was published over and is dropped.
	superseded := stageBackup(t, dir, "two", "proj-2", "old", 200)
	live := filepath.Join(dir, "proj-2")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "a.txt"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}

	recoverBundleDir(dir, nil)

	if got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt")); err != nil || string(got) != "v1" {
		t.Errorf("proj-1 should be restored: got %q err %v", got, err)
	}
	if _, err := os.Stat(restorable); !os.IsNotExist(err) {
		t.Errorf("proj-1 staging should be cleared, got %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(live, "a.txt")); err != nil || string(got) != "live" {
		t.Errorf("proj-2's live tree must win: got %q err %v", got, err)
	}
	if _, err := os.Stat(superseded); !os.IsNotExist(err) {
		t.Errorf("proj-2's superseded staging should be removed, got %v", err)
	}
}

// Recovery runs on every start, so a second pass over an already-repaired
// directory must be a no-op rather than treating the tree it restored last time
// as something to move again.
func TestRecoverBundleDirIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	stageBackup(t, dir, "one", "proj-1", "v1", 100)

	recoverBundleDir(dir, nil)
	first, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	recoverBundleDir(dir, nil)
	second, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt")); err != nil || string(got) != "v1" {
		t.Fatalf("the restored tree must survive a second pass: got %q err %v", got, err)
	}
	names := func(es []os.DirEntry) []string {
		out := []string{}
		for _, e := range es {
			out = append(out, e.Name())
		}
		return out
	}
	if !slices.Equal(names(first), names(second)) {
		t.Errorf("a second recovery pass changed the directory: %v then %v", names(first), names(second))
	}
}

// Two backups can both carry names recovery cannot order: v0.8.0 residue, or a
// crash before the marker. Recovery restores one of them and must then keep the
// other rather than deleting it as superseded, because no order exists to say
// which came first.
func TestRecoverBundleDirKeepsABackupItCannotTellApartFromTheRestore(t *testing.T) {
	dir := t.TempDir()
	first := stageBackup(t, dir, "one", "proj-1", "v1", 0)
	second := stageBackup(t, dir, "two", "proj-1", "v2", 0)

	recoverBundleDir(dir, nil)

	got, err := os.ReadFile(filepath.Join(dir, "proj-1", "a.txt"))
	if err != nil {
		t.Fatalf("nothing was restored: %v", err)
	}
	kept := 0
	for _, staging := range []string{first, second} {
		for _, at := range []string{staging, parkedStaging(staging)} {
			if _, err := os.Stat(filepath.Join(at, "backup", "a.txt")); err == nil {
				kept++
			}
		}
	}
	if kept != 1 {
		t.Fatalf("restored %q and kept %d of the two tied backups, want exactly 1 kept", got, kept)
	}
}

// ---- filesystem seam -------------------------------------------------------

// injectFault swaps one field of stagingFS for a wrapper that fails the call
// whose arguments satisfy match and passes every other call through to the real
// primitive. The call ordinal is appended to the arguments handed to match, so
// nthCall can select by call order without a second counter, and it is counted
// with an atomic because the racing scenarios drive one seam from two
// goroutines. The whole struct is restored in t.Cleanup, so a test that injects
// twice unwinds in reverse.
func injectFault(t *testing.T, step string, match func(args ...string) bool, err error) {
	t.Helper()
	var calls atomic.Int64
	real := stagingFS
	t.Cleanup(func() { stagingFS = real })
	fire := func(args ...string) bool {
		n := calls.Add(1)
		if match == nil {
			return true
		}
		return match(append(args, strconv.FormatInt(n, 10))...)
	}
	switch step {
	case "rename":
		stagingFS.rename = func(from, to string) error {
			if fire(from, to) {
				return err
			}
			return real.rename(from, to)
		}
	case "removeAll":
		stagingFS.removeAll = func(path string) error {
			if fire(path) {
				return err
			}
			return real.removeAll(path)
		}
	case "stat":
		stagingFS.stat = func(name string) (os.FileInfo, error) {
			if fire(name) {
				return nil, err
			}
			return real.stat(name)
		}
	case "lstat":
		stagingFS.lstat = func(name string) (os.FileInfo, error) {
			if fire(name) {
				return nil, err
			}
			return real.lstat(name)
		}
	case "readDir":
		stagingFS.readDir = func(name string) ([]os.DirEntry, error) {
			if fire(name) {
				return nil, err
			}
			return real.readDir(name)
		}
	case "readFile":
		stagingFS.readFile = func(name string) ([]byte, error) {
			if fire(name) {
				return nil, err
			}
			return real.readFile(name)
		}
	case "mkdir":
		stagingFS.mkdir = func(name string, perm os.FileMode) error {
			if fire(name) {
				return err
			}
			return real.mkdir(name, perm)
		}
	case "writeFile":
		stagingFS.writeFile = func(name string, data []byte, perm os.FileMode) error {
			if fire(name) {
				return err
			}
			return real.writeFile(name, data, perm)
		}
	case "create":
		stagingFS.create = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			if fire(name) {
				return nil, err
			}
			return real.create(name, flag, perm)
		}
	case "createTemp":
		stagingFS.createTemp = func(dir, pattern string) (*os.File, error) {
			if fire(dir, pattern) {
				return nil, err
			}
			return real.createTemp(dir, pattern)
		}
	default:
		t.Fatalf("injectFault: unknown step %q", step)
	}
}

// nthCall selects the nth call through an injected seam. It reads the ordinal
// injectFault appends to the arguments rather than counting for itself, so the
// two share one counter and a concurrent test has one place to be correct.
func nthCall(n int) func(args ...string) bool {
	return func(args ...string) bool {
		return len(args) > 0 && args[len(args)-1] == strconv.Itoa(n)
	}
}

// blockStep holds every call to step until the returned release runs, which is
// how a test parks one transaction mid-step and lets another reach the same
// destination. Releasing twice is safe, so a test can release on the happy path
// and still defer it.
func blockStep(t *testing.T, step string) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	injectFaultGate(t, step, gate)
	return release
}

// injectFaultGate is blockStep's half of the swap, split out so the wrapper it
// installs sits beside the fault wrappers above.
func injectFaultGate(t *testing.T, step string, gate <-chan struct{}) {
	t.Helper()
	real := stagingFS
	t.Cleanup(func() { stagingFS = real })
	switch step {
	case "rename":
		stagingFS.rename = func(from, to string) error {
			<-gate
			return real.rename(from, to)
		}
	case "removeAll":
		stagingFS.removeAll = func(path string) error {
			<-gate
			return real.removeAll(path)
		}
	case "stat":
		stagingFS.stat = func(name string) (os.FileInfo, error) {
			<-gate
			return real.stat(name)
		}
	default:
		t.Fatalf("blockStep: unknown step %q", step)
	}
}

// The seam has to be the real filesystem until a test swaps a field. A nil field
// is a panic in whatever production path reaches it first, and a field that no
// longer behaves like its os function makes every test above it prove nothing.
func TestStagingFSDefaultsAreTheRealPrimitives(t *testing.T) {
	for name, missing := range map[string]bool{
		"rename":     stagingFS.rename == nil,
		"removeAll":  stagingFS.removeAll == nil,
		"stat":       stagingFS.stat == nil,
		"lstat":      stagingFS.lstat == nil,
		"readDir":    stagingFS.readDir == nil,
		"readFile":   stagingFS.readFile == nil,
		"mkdir":      stagingFS.mkdir == nil,
		"writeFile":  stagingFS.writeFile == nil,
		"create":     stagingFS.create == nil,
		"createTemp": stagingFS.createTemp == nil,
	} {
		if missing {
			t.Errorf("stagingFS.%s is nil", name)
		}
	}

	root := t.TempDir()
	dir := filepath.Join(root, "one")
	if err := stagingFS.mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "a.txt")
	if err := stagingFS.writeFile(file, []byte("v0"), 0o600); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if _, err := stagingFS.stat(file); err != nil {
		t.Fatalf("stat: %v", err)
	}
	if _, err := stagingFS.lstat(file); err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if got, err := stagingFS.readFile(file); err != nil || string(got) != "v0" {
		t.Fatalf("readFile = %q, %v, want %q", got, err, "v0")
	}
	entries, err := stagingFS.readDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Fatalf("readDir = %v, %v, want one entry a.txt", entries, err)
	}
	tmp, err := stagingFS.createTemp(dir, "seam-*")
	if err != nil {
		t.Fatalf("createTemp: %v", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}
	opened, err := stagingFS.create(tmpName, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close opened: %v", err)
	}
	moved := filepath.Join(root, "two")
	if err := stagingFS.rename(dir, moved); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := stagingFS.stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stat of the old name = %v, want not-exist", err)
	}
	if err := stagingFS.removeAll(moved); err != nil {
		t.Fatalf("removeAll: %v", err)
	}
	if _, err := stagingFS.stat(moved); !os.IsNotExist(err) {
		t.Fatalf("stat after removeAll = %v, want not-exist", err)
	}
}

// The matrix needs both selections: "fail the rename whose source is seq 2's
// backup" is an argument match, and "fail the second remove" is an ordinal one.
// An ordinal alone cannot express the first, and an argument match cannot
// express a step that runs twice on the same path.
func TestInjectFaultMatchesByArgumentAndByOrdinal(t *testing.T) {
	injected := errors.New("injected seam failure")
	root := t.TempDir()

	t.Run("by argument", func(t *testing.T) {
		injectFault(t, "rename", func(args ...string) bool {
			return strings.HasSuffix(args[0], filepath.Join("seq", "backup"))
		}, injected)
		for _, name := range []string{"one", "seq", "three"} {
			from := filepath.Join(root, name, "backup")
			if err := os.MkdirAll(from, 0o700); err != nil {
				t.Fatal(err)
			}
			err := stagingFS.rename(from, filepath.Join(root, name, "moved"))
			if name == "seq" {
				if !errors.Is(err, injected) {
					t.Fatalf("rename of %s = %v, want the injected failure", from, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("rename of %s = %v, want it to pass through", from, err)
			}
		}
	})

	t.Run("by ordinal", func(t *testing.T) {
		injectFault(t, "removeAll", nthCall(2), injected)
		for i := 1; i <= 3; i++ {
			dir := filepath.Join(root, fmt.Sprintf("remove-%d", i))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			err := stagingFS.removeAll(dir)
			if i == 2 {
				if !errors.Is(err, injected) {
					t.Fatalf("removeAll call %d = %v, want the injected failure", i, err)
				}
				if _, statErr := os.Stat(dir); statErr != nil {
					t.Fatalf("the failed call must not have removed %s: %v", dir, statErr)
				}
				continue
			}
			if err != nil {
				t.Fatalf("removeAll call %d = %v, want it to pass through", i, err)
			}
			if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
				t.Fatalf("removeAll call %d left %s behind: %v", i, dir, statErr)
			}
		}
	})

	if got, want := reflect.ValueOf(stagingFS.removeAll).Pointer(), reflect.ValueOf(os.RemoveAll).Pointer(); got != want {
		t.Fatal("stagingFS.removeAll was not restored after the subtest's cleanup")
	}
	if got, want := reflect.ValueOf(stagingFS.rename).Pointer(), reflect.ValueOf(os.Rename).Pointer(); got != want {
		t.Fatal("stagingFS.rename was not restored after the subtest's cleanup")
	}

	// One seam, two goroutines: the ordinal must come from a counter that is
	// safe to increment concurrently, or the racing rows this seam exists for
	// report a race instead of a result.
	t.Run("concurrently", func(t *testing.T) {
		injectFault(t, "stat", nthCall(1), injected)
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, errs[i] = stagingFS.stat(root)
			}(i)
		}
		close(start)
		wg.Wait()
		failed := 0
		for _, err := range errs {
			if errors.Is(err, injected) {
				failed++
			} else if err != nil {
				t.Fatalf("the call that was not selected = %v, want it to pass through", err)
			}
		}
		if failed != 1 {
			t.Fatalf("%d of 2 concurrent calls failed, want exactly 1", failed)
		}
	})
}

// A blocked step has to stay blocked: the racing rows park one transaction
// inside a step and only then let the second one reach the same destination.
func TestBlockStepHoldsTheCallUntilRelease(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "blocked")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	release := blockStep(t, "removeAll")
	done := make(chan error, 1)
	go func() { done <- stagingFS.removeAll(dir) }()
	select {
	case err := <-done:
		t.Fatalf("removeAll returned %v before release", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	release()
	if err := <-done; err != nil {
		t.Fatalf("removeAll after release: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the released call did not run: %v", err)
	}
}
