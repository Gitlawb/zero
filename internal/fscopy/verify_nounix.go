//go:build !unix

package fscopy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// noFollowOpenDir opens the directory named by path. On non-unix targets there
// is no portable openat + O_NOFOLLOW primitive, so the traversal falls back to
// pathname-based os.ReadDir (see CopyTree/hashTreeInto in fscopy.go). This
// helper exists so the cross-platform CopyTree/HashTree entry points can share
// a single shape; on non-unix it simply opens the dir for reading via os.Open.
func noFollowOpenDir(path string) (*os.File, error) {
	return os.Open(path)
}

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
	sortStrings(names)
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

// sortStrings sorts a slice of strings in place (insertion sort — directory
// entry counts are small).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
