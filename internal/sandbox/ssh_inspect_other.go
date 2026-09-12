//go:build !unix

package sandbox

import (
	"fmt"
	"os"
)

func openSSHInspectionFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SSH inspection requires a regular file")
	}
	// Nonblocking open prevents a replacement FIFO from waiting for a writer.
	// The caller checks the opened descriptor again before reading any bytes.
	return os.OpenFile(path, os.O_RDONLY|sshInspectionNonblock, 0)
}
