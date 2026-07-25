//go:build !linux

package execution

import (
	"errors"
	"io"
	"os/exec"
)

func startPTYProcess(_ *exec.Cmd, _ io.Writer) (io.WriteCloser, func(), error) {
	return nil, nil, errors.New("pty transport is unavailable on this platform")
}
