//go:build windows

package peermsg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func ensurePrivateDir(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(abs[len(volume):], string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		attributes, attrErr := windows.GetFileAttributes(windows.StringToUTF16Ptr(current))
		if attrErr != nil {
			if !os.IsNotExist(attrErr) {
				return attrErr
			}
			if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
				return err
			}
			attributes, attrErr = windows.GetFileAttributes(windows.StringToUTF16Ptr(current))
			if attrErr != nil {
				return attrErr
			}
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return fmt.Errorf("refusing non-directory or reparse-point runtime path %q", current)
		}
	}
	return nil
}
