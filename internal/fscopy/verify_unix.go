//go:build unix

package fscopy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

// noFollowOpenDir opens the directory named by path without following a final
// symlink: O_NOFOLLOW makes the open fail (ELOOP) if the final component is a
// symlink, and O_DIRECTORY rejects non-directories. The returned *os.File owns
// the fd and keeps it open for the duration of the traversal, so a symlink
// swapped into path after this open cannot redirect the recursive reads below.
func noFollowOpenDir(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// noFollowOpenDirAt opens the child directory named `name` relative to the
// already-open parent directory fd, without following a final symlink. This is
// the openat-based core that closes the TOCTOU window: the parent fd pins the
// directory identity, so the child path is resolved relative to the HELD parent
// rather than re-resolved from the filesystem root by pathname. O_NOFOLLOW
// refuses a symlink final component; O_DIRECTORY rejects non-directories.
func noFollowOpenDirAt(parent *os.File, name string) (*os.File, error) {
	pfd := int(parent.Fd())
	fd, err := unix.Openat(pfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name)), nil
}

// fstatDir returns the dev/inode identity of the already-open directory file.
// Used to detect that the directory held by `f` is the same one an earlier
// Lstat classified (defense against a swap before the fd was opened).
func fstatDir(f *os.File) (dev uint64, ino uint64, err error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return 0, 0, &os.PathError{Op: "fstat", Path: f.Name(), Err: err}
	}
	return uint64(st.Dev), uint64(st.Ino), nil
}

// dirEntryInfo describes one entry in a directory, obtained via fstatat against
// a held parent fd so the entry's type is read relative to the pinned parent
// (no pathname re-resolution). isDir distinguishes directories from regular
// files from symlinks; mode carries the permission bits for regular files.
type dirEntryInfo struct {
	name   string
	mode   uint32 // os.FileMode permission bits (regular files)
	isDir  bool
	isReg  bool
	isLink bool
	size   int64
	dev    uint64 // directories only — for swap detection
	ino    uint64 // directories only
}

// readDirAt enumerates the entries of the already-open directory `dir` and
// returns their classification via fstatat(dirfd, name, AT_SYMLINK_NOFOLLOW).
// Using fstatat against the held fd means each entry's type is read relative to
// the pinned parent directory — a sibling symlink swapped in after the readdir
// is classified without following it, and a directory entry swapped for a
// symlink is seen as a symlink (isLink), not followed.
func readDirAt(dir *os.File) ([]dirEntryInfo, error) {
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, &os.PathError{Op: "readdir", Path: dir.Name(), Err: err}
	}
	// Readdirnames does not guarantee sort order; sort for stable traversal.
	sort.Strings(names)
	out := make([]dirEntryInfo, 0, len(names))
	dirFd := int(dir.Fd())
	for _, name := range names {
		var st unix.Stat_t
		if err := unix.Fstatat(dirFd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, &os.PathError{Op: "fstatat", Path: filepath.Join(dir.Name(), name), Err: err}
		}
		mode := uint32(st.Mode)
		ei := dirEntryInfo{
			name: name,
			mode: mode & 0o777,
		}
		switch mode & unix.S_IFMT {
		case unix.S_IFDIR:
			ei.isDir = true
			ei.dev = uint64(st.Dev)
			ei.ino = uint64(st.Ino)
		case unix.S_IFREG:
			ei.isReg = true
			ei.size = int64(st.Size)
		case unix.S_IFLNK:
			ei.isLink = true
		}
		out = append(out, ei)
	}
	return out, nil
}

// openRegularReadAt opens a regular file named `name` relative to the held
// parent directory fd, without following a final symlink, and rejects any
// non-regular descriptor before a blocking read can occur.
//
// readDirAt classifies each entry with fstatat and marks regular files as
// isReg, but that classification and this open are two independent syscalls.
// A concurrently writable local source can swap the entry for a FIFO in the
// fstatat→openat window. O_NOFOLLOW refuses a trailing symlink but does NOT
// reject FIFOs, sockets, or devices, so a plain open would block indefinitely
// on a FIFO (open of a FIFO read end blocks until a writer opens it). We open
// with O_NONBLOCK so a FIFO/socket/device returns immediately instead of
// hanging, then fstat the open descriptor and reject anything that is not a
// regular file — closing the gap before the later io.Copy. The nonblocking
// flag is harmless on a regular file and is cleared on the returned *os.File
// so callers read normally.
func openRegularReadAt(parent *os.File, name string) (*os.File, error) {
	pfd := int(parent.Fd())
	fd, err := unix.Openat(pfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: filepath.Join(parent.Name(), name), Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "fstat", Path: filepath.Join(parent.Name(), name), Err: err}
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "openat", Path: filepath.Join(parent.Name(), name), Err: fmt.Errorf("not a regular file")}
	}
	// Drop O_NONBLOCK so the caller gets ordinary blocking semantics on the
	// descriptor; the guard above was the only reason it was needed. Cheap and
	// portable — a no-op equivalent on a regular file that was never nonblocking.
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "setnonblock", Path: filepath.Join(parent.Name(), name), Err: err}
	}
	return os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name)), nil
}

// copyTreeAt is the fd-held core of CopyTree on unix. parent is the already-open
// source directory fd; dst is the destination directory (already created). The
// traversal reads entries via readDirAt(parent) and recurses with
// noFollowOpenDirAt(parent, name) — never re-resolving a pathname from the
// filesystem root — so a directory swapped for a symlink after the parent fd
// was opened cannot redirect the copy outside the install root.
func copyTreeAt(parent *os.File, dst string) error {
	entries, err := readDirAt(parent)
	if err != nil {
		return err
	}
	for _, ei := range entries {
		if ei.name == ".git" {
			continue
		}
		switch {
		case ei.isLink:
			// Never recreate a symlink: it could point outside the install dir.
			continue
		case ei.isDir:
			childSrc, err := noFollowOpenDirAt(parent, ei.name)
			if err != nil {
				return err
			}
			childDst := filepath.Join(dst, ei.name)
			if err := os.MkdirAll(childDst, 0o755); err != nil {
				_ = childSrc.Close()
				return err
			}
			err = copyTreeAt(childSrc, childDst)
			_ = childSrc.Close()
			if err != nil {
				return err
			}
		case ei.isReg:
			if err := copyFileAt(parent, ei.name, filepath.Join(dst, ei.name), ei.mode, ei.size); err != nil {
				return err
			}
		default:
			// Skip FIFOs, sockets, devices.
			continue
		}
	}
	return nil
}

// copyFileAt copies a single regular file from the held parent directory (named
// `name`) to dst. The source is opened via openRegularReadAt and fstat'd on the
// open descriptor so a swapped symlink cannot be read; the destination is
// created without following a final symlink. The source permission bits are
// applied to the copy so executables stay executable.
func copyFileAt(parent *os.File, name, dst string, srcPerm uint32, srcSize int64) error {
	in, err := openRegularReadAt(parent, name)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return &os.PathError{Op: "copy", Path: in.Name(), Err: fmt.Errorf("not a regular file")}
	}
	out, err := openRegularWrite(dst, uint32(info.Mode().Perm()))
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// hashTreeAt is the fd-held core of HashTree on unix. parent is the already-open
// directory fd; rel is the skill-relative path of `parent` (for the hash
// header). The traversal reads entries via readDirAt(parent) and recurses with
// noFollowOpenDirAt, so the hashed tree is exactly the tree that copyTreeAt
// would install (both use the same fd-held enumeration).
func hashTreeAt(hasher io.Writer, parent *os.File, rel string) error {
	entries, err := readDirAt(parent)
	if err != nil {
		return err
	}
	for _, ei := range entries {
		if ei.name == ".git" {
			continue
		}
		childRel := rel
		if rel == "" {
			childRel = ei.name
		} else {
			childRel = rel + "/" + ei.name
		}
		switch {
		case ei.isLink:
			// Skipped by copyTreeAt, so excluded from the hash too.
			continue
		case ei.isDir:
			// Directory header: type tag + size 0 so a dir and a file with the
			// same name cannot collide, and every entry is self-delimiting.
			header := fmt.Sprintf("%s\x00dir\x000\x00", childRel)
			if _, err := io.WriteString(hasher, header); err != nil {
				return err
			}
			child, err := noFollowOpenDirAt(parent, ei.name)
			if err != nil {
				return err
			}
			err = hashTreeAt(hasher, child, childRel)
			_ = child.Close()
			if err != nil {
				return err
			}
		case ei.isReg:
			if err := hashFileAt(hasher, parent, ei.name, childRel); err != nil {
				return err
			}
		default:
			continue
		}
	}
	return nil
}

// hashFileAt hashes a single regular file opened relative to the held parent fd.
func hashFileAt(hasher io.Writer, parent *os.File, name, rel string) error {
	file, err := openRegularReadAt(parent, name)
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
		return &os.PathError{Op: "hash", Path: file.Name(), Err: fmt.Errorf("not a regular file")}
	}
	perm := fileInfo.Mode().Perm()
	header := fmt.Sprintf("%s\x00file\x00%04o\x00%d\x00", rel, perm, fileInfo.Size())
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
		return &os.PathError{Op: "hash", Path: file.Name(), Err: fmt.Errorf("file changed while hashing")}
	}
	return file.Close()
}
