//go:build linux || darwin || freebsd || netbsd

package fsutil

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func preserveXattrs(f *os.File, srcPath string) error {
	names, err := listXattrs(srcPath)
	if err != nil {
		if isXattrUnsupported(err) {
			return nil
		}
		return fmt.Errorf("fsutil: listing xattrs of %s: %w", srcPath, err)
	}
	for _, name := range names {
		data, err := getXattr(srcPath, name)
		if err != nil {
			if isXattrUnsupported(err) {
				continue
			}
			return fmt.Errorf("fsutil: reading xattr %s from %s: %w", name, srcPath, err)
		}
		if err := unix.Fsetxattr(int(f.Fd()), name, data, 0); err != nil {
			if name == "security.selinux" {
				continue
			}
			return fmt.Errorf("fsutil: preserving xattr %s: %w", name, err)
		}
	}
	return nil
}

func listXattrs(path string) ([]string, error) {
	dest := []byte(nil)
	for {
		size, err := unix.Listxattr(path, dest)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return nil, nil
		}
		if len(dest) < size {
			dest = make([]byte, size)
			continue
		}
		return splitXattrNames(dest[:size]), nil
	}
}

func getXattr(path, name string) ([]byte, error) {
	dest := []byte(nil)
	for {
		size, err := unix.Getxattr(path, name, dest)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return []byte{}, nil
		}
		if len(dest) < size {
			dest = make([]byte, size)
			continue
		}
		return dest[:size], nil
	}
}

func splitXattrNames(buf []byte) []string {
	names := make([]string, 0)
	start := 0
	for i, b := range buf {
		if b != 0 {
			continue
		}
		if i > start {
			names = append(names, string(buf[start:i]))
		}
		start = i + 1
	}
	if start < len(buf) {
		names = append(names, string(buf[start:]))
	}
	return names
}

func isXattrUnsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}
