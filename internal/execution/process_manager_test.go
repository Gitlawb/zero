package execution

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func processManagerRequest(root string, command *exec.Cmd) Request {
	return Request{
		Origin: OriginInteractiveCommand, Mode: ModeCaptured,
		Command:          Command{Name: command.Path, Args: command.Args[1:]},
		WorkingDirectory: root, WorkspaceRoots: []string{root},
		Approval: ApprovalContext{PolicyVersion: PolicyVersion},
	}
}

func TestProcessManagerRetainsAndContinuesWithStableIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses a POSIX shell")
	}
	root := t.TempDir()
	manager := NewProcessManager(ProcessManagerOptions{CompletedRetention: time.Second})
	command := exec.Command("/bin/sh", "-c", "printf first; sleep 0.05; printf second")
	request := Request{
		Origin: OriginInteractiveCommand, Mode: ModeCaptured,
		Command:          Command{Name: "/bin/sh", Args: []string{"-c", "printf first; sleep 0.05; printf second"}},
		WorkingDirectory: root, WorkspaceRoots: []string{root},
		Approval: ApprovalContext{PolicyVersion: PolicyVersion},
	}
	started, err := manager.Start(context.Background(), ProcessStart{
		Prepared: PreparedCommand{Command: command}, Request: request, CommandText: "test",
	}, time.Millisecond)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.Exited || started.ProcessID < 1000 {
		t.Fatalf("initial result = %#v, want retained process", started)
	}
	continued, err := manager.Continue(context.Background(), ProcessContinue{ProcessID: started.ProcessID, Wait: time.Second})
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if !continued.Exited || continued.ProcessID != started.ProcessID {
		t.Fatalf("continued result = %#v, want same completed process", continued)
	}
	combined := started.Output + continued.Output
	if strings.Count(combined, "first") != 1 || strings.Count(combined, "second") != 1 {
		t.Fatalf("output was lost or duplicated: %q", combined)
	}
	if manager.Len() != 0 {
		t.Fatalf("completed process still retained: %d", manager.Len())
	}
}

func TestProcessManagerInterruptsRetainedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses a POSIX shell")
	}
	root := t.TempDir()
	manager := NewProcessManager(ProcessManagerOptions{})
	command := exec.Command("/bin/sh", "-c", "sleep 30")
	request := Request{
		Origin: OriginInteractiveCommand, Mode: ModeCaptured,
		Command:          Command{Name: "/bin/sh", Args: []string{"-c", "sleep 30"}},
		WorkingDirectory: root, WorkspaceRoots: []string{root},
	}
	started, err := manager.Start(context.Background(), ProcessStart{Prepared: PreparedCommand{Command: command}, Request: request}, time.Millisecond)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stopped, err := manager.Continue(context.Background(), ProcessContinue{ProcessID: started.ProcessID, Interrupt: true, Wait: time.Second})
	if err != nil {
		t.Fatalf("Continue interrupt: %v", err)
	}
	if !stopped.Exited || !stopped.Interrupted {
		t.Fatalf("interrupt result = %#v", stopped)
	}
}

func TestManagedProcessTerminateSkipsReapedProcess(t *testing.T) {
	reaped := make(chan struct{})
	close(reaped)
	called := false
	process := &managedProcess{
		command: &exec.Cmd{Process: &os.Process{Pid: 4242}},
		reaped:  reaped,
		kill:    func(int) error { called = true; return nil },
	}

	process.terminate()
	if called {
		t.Fatal("terminate signaled an already-reaped process")
	}
}

func TestProcessManagerCancellationReportsInterrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses a POSIX shell")
	}
	root := t.TempDir()
	manager := NewProcessManager(ProcessManagerOptions{})
	command := exec.Command("/bin/sh", "-c", "sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := manager.Start(ctx, ProcessStart{
		Prepared: PreparedCommand{Command: command},
		Request:  processManagerRequest(root, command),
	}, time.Second)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !result.Exited || !result.Interrupted {
		t.Fatalf("cancelled result = %#v, want exited and interrupted", result)
	}
}

func TestProcessManagerObservesChangesMadeDuringTransportStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses a POSIX shell")
	}
	root := t.TempDir()
	target := filepath.Join(root, "created.txt")
	manager := NewProcessManager(ProcessManagerOptions{})
	manager.startTransport = func(command *exec.Cmd, output io.Writer, tty bool) (io.WriteCloser, bool, func(), error) {
		if err := os.WriteFile(target, []byte("created"), 0o600); err != nil {
			return nil, false, nil, err
		}
		return startProcessTransport(command, output, tty)
	}
	command := exec.Command("/bin/sh", "-c", "true")

	result, err := manager.Start(context.Background(), ProcessStart{
		Prepared: PreparedCommand{Command: command},
		Request:  processManagerRequest(root, command),
	}, time.Second)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "created.txt" || result.Changes[0].Kind != ChangeCreated {
		t.Fatalf("changes = %#v, want created.txt created", result.Changes)
	}
}

func TestProcessWaitsClampToEffectiveBounds(t *testing.T) {
	if got := clampInitialProcessWait(time.Minute); got != maxInteractiveYield {
		t.Fatalf("initial upper clamp = %v, want %v", got, maxInteractiveYield)
	}
	if got := clampInitialProcessWait(time.Millisecond); got != time.Millisecond {
		t.Fatalf("initial short wait = %v, want caller value", got)
	}
	if got := clampContinuationWait(time.Hour, true); got != maxEmptyPollYield {
		t.Fatalf("empty poll upper clamp = %v, want %v", got, maxEmptyPollYield)
	}
	if got := clampContinuationWait(time.Hour, false); got != maxInteractiveYield {
		t.Fatalf("stdin upper clamp = %v, want %v", got, maxInteractiveYield)
	}
}

func TestProcessOutputBufferCapsUndrainedData(t *testing.T) {
	buffer := newProcessOutputBuffer()
	chunk := []byte(strings.Repeat("x", 1024))
	for i := 0; i < maxPendingOutputBytes/len(chunk)+10; i++ {
		if _, err := buffer.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	buffer.mu.Lock()
	dataLen := len(buffer.data)
	buffer.mu.Unlock()
	if dataLen > maxPendingOutputBytes {
		t.Fatalf("undrained buffer grew to %d bytes, want <= %d", dataLen, maxPendingOutputBytes)
	}
	if got := buffer.drain(); !strings.HasSuffix(string(got), string(chunk)) {
		t.Fatal("drained output should retain the newest bytes")
	}
	if !buffer.peekTruncated() || !buffer.consumeTruncated() || buffer.consumeTruncated() {
		t.Fatal("truncation state was not preserved and consumed exactly once")
	}
}

func TestManagedProcessCollectRespectsDeadlineUnderContinuousOutput(t *testing.T) {
	process := &managedProcess{output: newProcessOutputBuffer(), done: make(chan struct{})}
	stop := make(chan struct{})
	var writers sync.WaitGroup
	for i := 0; i < 8; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			chunk := []byte(strings.Repeat("x", 256))
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = process.output.Write(chunk)
				}
			}
		}()
	}
	t.Cleanup(func() { close(stop); writers.Wait() })

	wait := 200 * time.Millisecond
	started := time.Now()
	_, _ = process.collect(context.Background(), wait)
	if elapsed := time.Since(started); elapsed > 5*wait {
		t.Fatalf("collect took %v under continuous output, want close to %v", elapsed, wait)
	}
}

func TestManagedProcessCollectCapsCumulativeOutput(t *testing.T) {
	process := &managedProcess{output: newProcessOutputBuffer(), done: make(chan struct{})}
	chunk := []byte(strings.Repeat("x", maxPendingOutputBytes/2))
	go func() {
		for i := 0; i < 5; i++ {
			_, _ = process.output.Write(chunk)
			for {
				process.output.mu.Lock()
				drained := len(process.output.data) == 0
				process.output.mu.Unlock()
				if drained {
					break
				}
				runtime.Gosched()
			}
		}
		close(process.done)
	}()

	output, truncated := process.collect(context.Background(), time.Second)
	if len(output) > maxPendingOutputBytes {
		t.Fatalf("collected %d bytes, want <= %d", len(output), maxPendingOutputBytes)
	}
	if !truncated {
		t.Fatal("cumulative output cap must report truncation")
	}
}

func TestProcessPruningDoesNotRaceTouch(t *testing.T) {
	manager := NewProcessManager(ProcessManagerOptions{})
	for id := 1000; id < 1012; id++ {
		manager.processes[id] = &managedProcess{id: id, lastUsedAt: time.Now(), done: make(chan struct{})}
	}
	target := manager.processes[1000]
	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for {
			select {
			case <-stop:
				return
			default:
				target.touch()
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		manager.mu.Lock()
		_ = manager.processToPruneLocked()
		manager.mu.Unlock()
	}
	close(stop)
	writer.Wait()
}
