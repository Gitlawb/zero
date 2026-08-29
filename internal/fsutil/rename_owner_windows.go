//go:build windows

package fsutil

import "os"

func preserveOwner(*os.File, os.FileInfo) error {
	return nil
}
