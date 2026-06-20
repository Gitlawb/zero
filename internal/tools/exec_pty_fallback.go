//go:build !linux

package tools

import (
	"errors"
	"io"
	"os/exec"
)

var errPTYUnavailable = errors.New("pty transport is unavailable on this platform")

func startPTYProcess(_ *exec.Cmd, _ *execOutputBuffer) (io.WriteCloser, func(), error) {
	return nil, nil, errPTYUnavailable
}
