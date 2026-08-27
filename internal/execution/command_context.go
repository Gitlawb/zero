package execution

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// RunCommand runs a context-bound command in a retained process tree and
// prevents inherited output handles from blocking Wait indefinitely.
func RunCommand(ctx context.Context, command *exec.Cmd) (err error) {
	if command == nil {
		return errors.New("execution: nil command")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tree, err := prepareCommandTree(command)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tree.close()) }()

	command.WaitDelay = processWaitDelay
	command.Cancel = tree.cancel
	if err := command.Start(); err != nil {
		_ = tree.attach(nil)
		return err
	}
	if err := tree.attach(command.Process); err != nil {
		killErr := command.Process.Kill()
		waitErr := command.Wait()
		return errors.Join(fmt.Errorf("execution: attach process tree: %w", err), killErr, waitErr)
	}
	waitComplete := make(chan struct{})
	type cancellation struct {
		err      error
		canceled bool
	}
	cancelResult := make(chan cancellation, 1)
	go func() {
		select {
		case <-ctx.Done():
			cancelResult <- cancellation{err: tree.cancel(), canceled: true}
		case <-waitComplete:
			cancelResult <- cancellation{}
		}
	}()
	waitErr := command.Wait()
	close(waitComplete)
	canceled := <-cancelResult
	if canceled.canceled {
		return errors.Join(waitErr, ctx.Err(), canceled.err)
	}
	return waitErr
}
