package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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
	real := renameDir
	renameDir = func(from, to string) error {
		time.Sleep(2 * time.Millisecond)
		return real(from, to)
	}
	t.Cleanup(func() { renameDir = real })

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
	real := renameDir
	calls := 0
	renameDir = func(from, to string) error {
		calls++
		if calls == 1 {
			return errors.New("injected publish failure")
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameDir = real })

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

	real := renameDir
	renameDir = func(string, string) error { return errors.New("injected rename failure") }
	t.Cleanup(func() { renameDir = real })

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
}
