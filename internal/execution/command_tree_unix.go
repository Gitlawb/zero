//go:build !windows

package execution

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type commandTree struct {
	ready chan struct{}
	pid   int
}

func prepareCommandTree(command *exec.Cmd) (*commandTree, error) {
	ConfigureProcessGroup(command)
	return &commandTree{ready: make(chan struct{})}, nil
}

func (tree *commandTree) attach(process *os.Process) error {
	if process != nil {
		tree.pid = process.Pid
	}
	close(tree.ready)
	return nil
}

func (tree *commandTree) cancel() error {
	<-tree.ready
	if tree.pid <= 1 {
		return nil
	}
	if err := syscall.Kill(-tree.pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (tree *commandTree) close() error { return tree.cancel() }
