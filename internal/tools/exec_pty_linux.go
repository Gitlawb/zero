//go:build linux

package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func startPTYProcess(command *exec.Cmd, output *execOutputBuffer) (io.WriteCloser, func(), error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = master.Close()
		_ = slave.Close()
	}
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	hardenPTYProcessLifetime(command, slave)
	if err := command.Start(); err != nil {
		cleanup()
		return nil, nil, err
	}
	_ = slave.Close()
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(output, master)
	}()
	return master, func() {
		// cleanup runs in the Wait goroutine AFTER command.Wait() returns, so the
		// child has exited and its slave fds are closed — the master then EOFs once
		// io.Copy drains the remaining PTY output into the buffer. Wait for that copy
		// to finish BEFORE closing the master and letting the caller mark the session
		// done + remove it; otherwise a command's final output chunk (e.g. a test
		// runner's last PASS/FAIL line) can be lost on exit. Closing the master first
		// would truncate the unread tail, so the join precedes the Close. Bounded so a
		// stuck copy can't hang teardown. (AUDIT-M14)
		select {
		case <-copied:
		case <-time.After(bashWaitDelay):
		}
		_ = master.Close()
	}, nil
}

func openPTY() (*os.File, *os.File, error) {
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	if err := unix.IoctlSetPointerInt(masterFD, unix.TIOCSPTLCK, 0); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	pts, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	slaveName := fmt.Sprintf("/dev/pts/%d", pts)
	slaveFD, err := unix.Open(slaveName, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	slave := os.NewFile(uintptr(slaveFD), slaveName)
	return master, slave, nil
}

func hardenPTYProcessLifetime(command *exec.Cmd, slave *os.File) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setsid = true
	command.SysProcAttr.Setctty = true
	command.SysProcAttr.Ctty = 0
	command.WaitDelay = bashWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}
