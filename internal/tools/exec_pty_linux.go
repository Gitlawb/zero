//go:build linux

package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

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
	go func() {
		_, _ = io.Copy(output, master)
	}()
	return master, func() { _ = master.Close() }, nil
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
