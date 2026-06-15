package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
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
