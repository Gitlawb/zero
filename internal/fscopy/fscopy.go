// Package fscopy provides safe directory-tree copy and content-hash utilities.
// It is shared by the plugin installer and the skill installer so security
// properties (skip .git, reject symlinks, skip non-regular files, deterministic
// sort order) are defined once.
//
// On unix (linux/darwin) the traversal is fd-held: the root directory is opened
// once with O_NOFOLLOW|O_DIRECTORY, entries are enumerated via fstatat against
// the held fd, and subdirectories are opened with openat(parentFd, name,
// O_NOFOLLOW|O_DIRECTORY) and recursed with that fd. The directory identity is
// therefore pinned for the whole traversal — a directory swapped for a symlink
// after the parent fd was opened cannot redirect the copy or hash outside the
// source root. On non-unix targets (Plan 9, WASM) the traversal falls back to
// pathname-based os.ReadDir; Windows uses the open_windows.go helper for file
// opens and a pathname-based dir walk (no portable openat equivalent).
package fscopy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// CopyTree recursively copies regular files and directories from src to dst. It
// skips the .git directory (clone metadata) and refuses symlinks so a malicious
// source cannot smuggle a link that escapes the install dir. Copying is pure
// I/O — it never executes anything it copies.
//
// On unix the traversal is fd-held (see the package doc): the root is opened
// with O_NOFOLLOW|O_DIRECTORY and recursion uses openat against the held parent
// fd, so a source directory swapped for a symlink mid-traversal cannot redirect
// the copy outside src. On non-unix the traversal is pathname-based.
func CopyTree(src string, dst string) error {
	root, err := noFollowOpenDir(src)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return copyTreeAt(root, dst)
}

// CopyFile copies a single regular file from src to dst. The source is opened
// and then fstat'd on the already-open descriptor, so the kind and permission
// bits come from the fd actually read — a symlink or other file type swapped in
// after the open cannot be read or mis-classified (no TOCTOU). The destination
// is created or truncated without following a final symlink (O_NOFOLLOW on
// unix, FILE_FLAG_OPEN_REPARSE_POINT on Windows), so a pre-placed destination
// symlink cannot redirect the copy outside the install dir. The source's
// permission bits are applied to the copy so executables stay executable.
// Copying is pure I/O — it never executes anything.
func CopyFile(src string, dst string) error {
	in, err := openRegularRead(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		// The entry was regular when listed but the open resolved something else
		// (e.g. a symlink or special file); refuse rather than copy it.
		return &os.PathError{Op: "copy", Path: src, Err: fmt.Errorf("not a regular file")}
	}
	out, err := openRegularWrite(dst, uint32(info.Mode().Perm()))
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	// openRegularWrite applies perm & ~umask, which can drop requested bits;
	// chmod the open descriptor so the source mode is preserved exactly.
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// HashTree computes a content hash over the same filtered tree that CopyTree
// installs: regular files only, .git and symlinks skipped, walked in a stable
// sorted order. Each file contributes its relative path, executable bit, size,
// and bytes, so renames, mode flips, size changes, and content edits all change
// the hash, and the stream is self-delimiting (no two trees collide by shifting
// bytes across file boundaries).
//
// On unix the traversal is fd-held and uses the same openat-based enumeration as
// CopyTree, so the hashed tree is exactly the tree CopyTree installs.
func HashTree(root string) (string, error) {
	dir, err := noFollowOpenDir(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	hasher := sha256.New()
	if err := hashTreeAt(hasher, dir, ""); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
