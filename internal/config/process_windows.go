//go:build windows

package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configureCommandProcess starts the process suspended so it can be
// assigned to the job object before its main thread (and therefore any
// code it runs) executes. Without this, a fast command can spawn and
// detach a descendant before AssignProcessToJobObject runs, letting that
// descendant escape the job and survive termination.
func configureCommandProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
}

// commandProcess tracks a started provider command so its entire process
// tree can be terminated on timeout. It prefers a job object: taskkill /T
// walks the tree by parent PID in user space and can miss descendants,
// letting them run to completion while Wait blocks on inherited pipes.
type commandProcess struct {
	cmd *exec.Cmd
	job windows.Handle
}

func attachCommandProcess(cmd *exec.Cmd) *commandProcess {
	proc := &commandProcess{cmd: cmd}
	if cmd.Process == nil {
		return proc
	}
	// The main thread is suspended (see configureCommandProcess); resume it
	// once we're done attaching, however that turns out.
	defer resumeMainThread(cmd.Process.Pid)

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return proc
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return proc
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		_ = windows.CloseHandle(job)
		return proc
	}
	proc.job = job
	return proc
}

// resumeMainThread resumes the (assumed suspended) primary thread of pid.
// It's a no-op if the thread can't be found or is already running.
func resumeMainThread(pid int) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			continue
		}
		_, _ = windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
	}
}

func (p *commandProcess) Terminate() {
	if p.job != 0 {
		_ = windows.TerminateJobObject(p.job, 1)
	}
	if p.cmd.Process == nil {
		return
	}
	// Fallback for descendants spawned before the job assignment or when
	// job creation failed.
	taskkill := taskkillPath()
	_ = exec.Command(taskkill, "/T", "/F", "/PID", strconv.Itoa(p.cmd.Process.Pid)).Run()
	_ = p.cmd.Process.Kill()
}

// Close releases the job handle without touching any still-running
// descendants: the job carries no KILL_ON_JOB_CLOSE limit, so on the
// success path a provider command's detached helpers keep running exactly
// as they did before job objects were introduced. Descendant termination
// happens explicitly via Terminate, called only on timeout/error.
func (p *commandProcess) Close() {
	if p.job == 0 {
		return
	}
	_ = windows.CloseHandle(p.job)
	p.job = 0
}

func taskkillPath() string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = os.Getenv("windir")
	}
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	return filepath.Join(systemRoot, "System32", "taskkill.exe")
}
