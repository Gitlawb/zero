//go:build !unix

// This file is the !unix fallback for CopyTree/HashTree traversal. On unix
// (linux/darwin — the primary install targets, and what CI and local dev run)
// the same functions live in verify_unix.go and are fd-held: the root is opened
// with O_NOFOLLOW|O_DIRECTORY and recursion uses openat against the held parent
// fd, so a source directory swapped for a symlink mid-traversal cannot redirect
// the copy or hash outside src. That fd-held path has NO TOCTOU window.
//
// This !unix fallback is pathname-based instead, because there is no portable
// openat primitive on these targets. It re-resolves child paths by name. Two
// residual TOCTOU windows follow from that — both require an attacker with
// write access to the source tree racing the traversal, so they are not
// remotely reachable; the practical impact is also bounded:
//
//   - copyTreeAt: noFollowOpenDir already re-validates reparse points on every
//     child directory it opens, so a child directory swapped for a junction is
//     rejected. The remaining window is between opening+verifying a directory
//     handle and the os.ReadDir(parent.Name()) that lists it by path — a
//     sub-microsecond race on an already-verified directory.
//   - hashTreeIntoPath: does NOT re-open child directories through
//     noFollowOpenDir; it lists and stats by path, so a child directory swapped
//     for a junction is recursed into as if ordinary. This window is wider, but
//     it affects the recorded skills.lock hash, not the bytes copied (copyTreeAt
//     still refuses a junction at open time). The worst case is a lockfile hash
//     that does not match the installed tree, not installation of attacker
//     content.
//
// TODO(windows): replace this pathname-based traversal with a handle-held one on
// Windows (enumerate via the already-open directory handle, e.g.
// NtQueryDirectoryFile/FindFirstFileEx on the handle, and recurse through
// handles rather than re-resolved paths) so the !unix fallback only covers
// Plan 9/WASM, which are not supported install targets. Requires a Windows
// environment to develop and verify; deliberately not done blind.

package fscopy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// copyTreeAt is the non-unix fallback: pathname-based recursive copy. There is
// no portable openat primitive on these targets, so the TOCTOU-narrowing that
// the unix fd-held traversal provides is not available here; the traversal
// re-resolves child paths by name (the same behavior as before this refactor).
// Install flows run on unix (linux/darwin) and windows; Plan 9/WASM are not
// supported install targets.
func copyTreeAt(parent *os.File, dst string) error {
	entries, err := os.ReadDir(parent.Name())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		srcPath := filepath.Join(parent.Name(), name)
		dstPath := filepath.Join(dst, name)
		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			continue
		case info.IsDir():
			if err := copyTreeInto(srcPath, dstPath); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err := CopyFile(srcPath, dstPath); err != nil {
				return err
			}
		default:
			continue
		}
	}
	return nil
}

// copyTreeInto is a thin wrapper keeping the non-unix fallback self-contained.
func copyTreeInto(src, dst string) error {
	f, err := noFollowOpenDir(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return copyTreeAt(f, dst)
}

// hashTreeAt is the non-unix fallback: pathname-based recursive hash.
func hashTreeAt(hasher io.Writer, parent *os.File, rel string) error {
	return hashTreeIntoPath(hasher, parent.Name(), parent.Name())
}

// hashTreeIntoPath mirrors the original pathname-based hashTreeInto for the
// non-unix fallback. It is kept here (not in fscopy.go) so the cross-platform
// file stays focused on the public API.
func hashTreeIntoPath(hasher io.Writer, root, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if name == ".git" {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			continue
		case info.IsDir():
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			header := fmt.Sprintf("%s\x00dir\x000\x00", filepath.ToSlash(rel))
			if _, err := io.WriteString(hasher, header); err != nil {
				return err
			}
			if err := hashTreeIntoPath(hasher, root, path); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			file, err := openRegularRead(path)
			if err != nil {
				return err
			}
			fileInfo, err := file.Stat()
			if err != nil {
				_ = file.Close()
				return err
			}
			if !fileInfo.Mode().IsRegular() {
				_ = file.Close()
				return &os.PathError{Op: "hash", Path: path, Err: fmt.Errorf("not a regular file")}
			}
			perm := fileInfo.Mode().Perm()
			header := fmt.Sprintf("%s\x00file\x00%04o\x00%d\x00", filepath.ToSlash(rel), perm, fileInfo.Size())
			if _, err := io.WriteString(hasher, header); err != nil {
				_ = file.Close()
				return err
			}
			written, err := io.Copy(hasher, file)
			if err != nil {
				_ = file.Close()
				return err
			}
			if written != fileInfo.Size() {
				_ = file.Close()
				return &os.PathError{Op: "hash", Path: path, Err: fmt.Errorf("file changed while hashing")}
			}
			if err := file.Close(); err != nil {
				return err
			}
		default:
			continue
		}
	}
	return nil
}
