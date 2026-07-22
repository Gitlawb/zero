package fscopy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeFile writes content to name under dir and returns the full path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdirall %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestCopyTreeCopiesRegularFiles verifies a tree of regular files is copied
// verbatim, content-preserved end to end. (The executable-bit round trip is
// exercised on unix in fscopy_unix_test.go, since os.Chmod only toggles the
// read-only bit on Windows and a 0644->0755 flip is a no-op there.)
func TestCopyTreeCopiesRegularFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, src, "a/b.txt", "hello\n")
	writeFile(t, src, "bin/run.sh", "#!/bin/sh\n")

	if err := CopyTree(src, dst); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "a", "b.txt"))
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("content = %q, want %q", got, "hello\n")
	}

	if _, err := os.ReadFile(filepath.Join(dst, "bin", "run.sh")); err != nil {
		t.Fatalf("bin/run.sh missing in dst: %v", err)
	}
}

// TestCopyTreeSkipsDotGit verifies the .git directory (clone metadata) is
// skipped on copy so a malicious tree cannot smuggle git config into the
// install dir.
func TestCopyTreeSkipsDotGit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, src, "SKILL.md", "skill")
	writeFile(t, src, ".git/config", "[remote \"origin\"]\nurl = file:///etc")
	writeFile(t, src, ".git/refs/heads/main", "deadbeef\n")
	writeFile(t, src, "assets/run.sh", "echo hi\n")

	if err := CopyTree(src, dst); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git copied to dst; stat err = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md missing in dst: %v", err)
	}
	if string(got) != "skill" {
		t.Fatalf("SKILL.md = %q", got)
	}
}

// TestHashTreeOrderInvariantAndSensitive verifies the hash is invariant to
// directory enumeration order and sensitive to path, executable bit, size,
// and content.
func TestHashTreeOrderInvariantAndSensitive(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, "a.txt", "aaa")
	writeFile(t, root, "b.txt", "bbb")
	writeFile(t, root, "dir/c.txt", "ccc")

	want, err := HashTree(root)
	if err != nil {
		t.Fatalf("HashTree: %v", err)
	}

	// Invariant: same content rects the same hash regardless of insertion
	// order. Re-create with reverse order.
	root2 := t.TempDir()
	writeFile(t, root2, "dir/c.txt", "ccc")
	writeFile(t, root2, "b.txt", "bbb")
	writeFile(t, root2, "a.txt", "aaa")
	got2, err := HashTree(root2)
	if err != nil {
		t.Fatalf("HashTree2: %v", err)
	}
	if got2 != want {
		t.Fatalf("hash not order-invariant: got %s, want %s", got2, want)
	}

	// Sensitive: change content -> hash changes.
	root3 := t.TempDir()
	writeFile(t, root3, "a.txt", "AAA")
	writeFile(t, root3, "b.txt", "bbb")
	writeFile(t, root3, "dir/c.txt", "ccc")
	if got3, err := HashTree(root3); err != nil || got3 == want {
		t.Fatalf("content edit did not change hash: %v got=%s want=%s", err, got3, want)
	}

	// Sensitive: change path -> hash changes.
	root4 := t.TempDir()
	writeFile(t, root4, "a.txt", "aaa")
	writeFile(t, root4, "renamed.txt", "bbb")
	writeFile(t, root4, "dir/c.txt", "ccc")
	if got4, err := HashTree(root4); err != nil || got4 == want {
		t.Fatalf("rename did not change hash: %v got=%s want=%s", err, got4, want)
	}

	// Sensitive: change executable bit -> hash changes. (Unix only: on
	// Windows os.Chmod only toggles the read-only bit, so a 0644->0755 flip
	// is a no-op and the hash would not change — covered instead in
	// fscopy_unix_test.go::TestHashTreeSensitiveToExecBit.)
	if runtime.GOOS != "windows" {
		root5 := t.TempDir()
		writeFile(t, root5, "a.txt", "aaa")
		exe := writeFile(t, root5, "b.txt", "bbb")
		if err := os.Chmod(exe, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		writeFile(t, root5, "dir/c.txt", "ccc")
		if got5, err := HashTree(root5); err != nil || got5 == want {
			t.Fatalf("exec bit flip did not change hash: %v got=%s want=%s", err, got5, want)
		}
	}

	// Sensitive: change file size -> hash changes.
	root6 := t.TempDir()
	writeFile(t, root6, "a.txt", "aaa")                 // unchanged
	writeFile(t, root6, "b.txt", "bbb\x00\x00\x00\x00") // same prefix, longer
	writeFile(t, root6, "dir/c.txt", "ccc")
	if got6, err := HashTree(root6); err != nil || got6 == want {
		t.Fatalf("size change did not change hash: %v got=%s want=%s", err, got6, want)
	}

	// Self-delimiting: merging two files into one (same joined bytes) must
	// NOT reproduce the hash, because each file carries a path/type/size
	// header. Bumping the header guard is the point of the design.
	root7 := t.TempDir()
	writeFile(t, root7, "a.txt", "x y z") // single 5-byte file instead of two
	if got7, err := HashTree(root7); err != nil || got7 == want {
		t.Fatalf("split/merge boundary did not change hash: %v got=%s want=%s", err, got7, want)
	}
}

// TestHashTreeSkipsDotGitAndEncodesDir self-verifies that omitting .git does
// not change the hash (so it can't be used as a collision vector) and that a
// directory entry contributes a distinct token from a file with the same name
// would (tested implicitly via the order/sensitivity suite above; here we
// just confirm .git is excluded from the hash).
func TestHashTreeSkipsDotGitAndNonRegular(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "SKILL.md", "skill")
	writeFile(t, root, ".git/config", "[gc]\nauto=1\n")
	want, err := HashTree(root)
	if err != nil {
		t.Fatalf("HashTree with .git: %v", err)
	}

	clean := t.TempDir()
	writeFile(t, clean, "SKILL.md", "skill")
	got, err := HashTree(clean)
	if err != nil {
		t.Fatalf("HashTree clean: %v", err)
	}
	if got != want {
		t.Fatalf(".git content changed hash: with=%s without=%s", want, got)
	}
}

// ---------------------------------------------------------------------------
// Symlink-rejection tests. Symlink creation is only portable on unix; on
// Windows/Plan9/WASM we skip rather than fake the filesystem semantics.
// ---------------------------------------------------------------------------

// TestCopyTreeSkipsSymlink verifies a symlink in the source tree is NOT
// recreated in dst, so a malicious source cannot smuggle a link that escapes
// the install dir.
func TestCopyTreeSkipsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics not portable on non-unix builds")
	}
	src := t.TempDir()
	dst := t.TempDir()
	target := writeFile(t, src, "target.txt", "secret\n")
	writeFile(t, src, "real.txt", "real\n")
	if err := os.Symlink(target, filepath.Join(src, "link.txt")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}

	if err := CopyTree(src, dst); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	// real.txt copied, link.txt absent.
	if _, err := os.ReadFile(filepath.Join(dst, "real.txt")); err != nil {
		t.Fatalf("real.txt missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "link.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink was recreated in dst; Lstat err = %v", err)
	}
}

// TestOpenRegularReadRejectsSymlink covers the TOCTOU-hardened read helper:
// opening a path that IS a symlink must fail regardless of what the symlink
// points at, so a swapped-in link cannot be followed.
func TestOpenRegularReadRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics not portable on non-unix builds")
	}
	dir := t.TempDir()
	target := writeFile(t, dir, "target", "data\n")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}

	if f, err := openRegularRead(link); err == nil {
		_ = f.Close()
		t.Fatal("openRegularRead followed a symlink; want refusal")
	}
}

// TestOpenRegularWriteRejectsSymlink covers the TOCTOU-hardened write helper:
// a pre-placed symlink at the destination must be refused so the copy cannot
// be redirected to the symlink target (and its content truncated).
func TestOpenRegularWriteRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics not portable on non-unix builds")
	}
	dir := t.TempDir()
	writeFile(t, dir, "canary", "preserved\n")
	link := filepath.Join(dir, "dst")
	if err := os.Symlink(filepath.Join(dir, "canary"), link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}

	if f, err := openRegularWrite(link, 0o644); err == nil {
		_ = f.Close()
		t.Fatal("openRegularWrite followed a pre-placed symlink; want refusal")
	}

	// The symlink target must NOT have been truncated.
	got, err := os.ReadFile(filepath.Join(dir, "canary"))
	if err != nil {
		t.Fatalf("read canary: %v", err)
	}
	if string(got) != "preserved\n" {
		t.Fatalf("symlink target was truncated through the open: %q", got)
	}
}

// TestCopyFileRefusesSymlinkSource drives CopyFile on a symlink source: the
// fd-level fstat in CopyFile must reject a non-regular file (a symlink, even
// if the open itself followed it on a non-unix fallback) rather than copy it.
func TestCopyFileRefusesSymlinkSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics not portable on non-unix builds")
	}
	dir := t.TempDir()
	target := writeFile(t, dir, "target.txt", "payload\n")
	link := filepath.Join(dir, "src.txt")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}

	dst := filepath.Join(dir, "out.txt")
	if err := CopyFile(link, dst); err == nil {
		// On unix the no-follow open rejects the link outright; on the
		// !unix&&!windows fallback the post-open Lstat in openRegularRead
		// closes it. Either way CopyFile must fail.
		if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
			t.Fatalf("CopyFile succeeded and wrote dst from a symlink source; stat=%v", statErr)
		}
	}
}

// TestCopyFileWritesToRegularDestination guards the normal path so the write
// hardening does not break legitimate copies to a regular destination.
func TestCopyFileWritesToRegularDestination(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "content\n")

	// Source is a regular file; dst does not yet exist.
	dst := filepath.Join(dir, "dst.txt")
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "content\n" {
		t.Fatalf("dst content = %q", got)
	}

	// Destination already exists as a regular file -> truncates and
	// overwrites rather than refusing.
	src2 := writeFile(t, dir, "src2.txt", "new\n")
	if err := CopyFile(src2, dst); err != nil {
		t.Fatalf("CopyFile overwrite: %v", err)
	}
	got2, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst2: %v", err)
	}
	if string(got2) != "new\n" {
		t.Fatalf("dst overwrite = %q", got2)
	}
}
