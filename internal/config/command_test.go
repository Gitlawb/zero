package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadProviderCommandSuccess(t *testing.T) {
	command := writeCommand(t, commandScript{
		Stdout: `{"name":"cmd","provider":"openai","apiKey":"sk-command","model":"gpt-command"}`,
	})

	cfg, err := LoadProviderCommand(command)
	if err != nil {
		t.Fatalf("LoadProviderCommand() error = %v", err)
	}

	if len(cfg.Providers) != 1 {
		t.Fatalf("providers length = %d, want 1", len(cfg.Providers))
	}
	provider := cfg.Providers[0]
	if provider.Name != "cmd" || provider.APIKey != "sk-command" || provider.Model != "gpt-command" {
		t.Fatalf("provider = %#v, want command provider", provider)
	}
}

func TestLoadProviderCommandDoesNotResolveAPIKeyEnvFromProcess(t *testing.T) {
	t.Setenv("ZERO_CMD_API_KEY", "sk-process")
	command := writeCommand(t, commandScript{
		Stdout: `{"name":"cmd","provider":"openai","apiKeyEnv":"ZERO_CMD_API_KEY","model":"gpt-command"}`,
	})

	cfg, err := LoadProviderCommand(command)
	if err != nil {
		t.Fatalf("LoadProviderCommand() error = %v", err)
	}

	provider := cfg.Providers[0]
	if provider.APIKey != "" {
		t.Fatalf("APIKey = %q, want unresolved provider-command apiKeyEnv", provider.APIKey)
	}
	if provider.APIKeyEnv != "ZERO_CMD_API_KEY" {
		t.Fatalf("APIKeyEnv = %q, want command apiKeyEnv preserved", provider.APIKeyEnv)
	}
}

func TestLoadProviderCommandFailureIncludesExitAndRedactsOutput(t *testing.T) {
	command := writeCommand(t, commandScript{
		Stderr:   "failed with sk-command-secret",
		ExitCode: 7,
	})

	_, err := LoadProviderCommand(command)
	if err == nil {
		t.Fatal("LoadProviderCommand() error = nil, want command failure")
	}
	if !strings.Contains(err.Error(), "provider command failed") || !strings.Contains(err.Error(), "exit status") {
		t.Fatalf("error = %q, want command failure with exit status", err.Error())
	}
	if strings.Contains(err.Error(), "sk-command-secret") {
		t.Fatalf("error leaked command secret: %q", err.Error())
	}
}

func TestLoadProviderCommandTimeout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "sleep.pid")
	command := writeCommand(t, commandScript{SleepSeconds: 10, PidFile: pidFile})

	start := time.Now()
	_, err := LoadProviderCommand(command)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("LoadProviderCommand() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out after 5s") {
		t.Fatalf("error = %q, want timeout", err.Error())
	}
	maxElapsed := 7 * time.Second
	if runtime.GOOS == "windows" {
		maxElapsed = 9 * time.Second
	}
	if elapsed > maxElapsed {
		t.Fatalf("timeout returned after %s, want roughly 5s", elapsed)
	}
	assertProcessTerminatedIfStarted(t, pidFile)
}

// providerCommandTestBudget bounds the two background-child tests below, and is
// deliberately NOT providerCommandTimeout. Those tests assert which error each
// cmd.Wait path yields and that the leftover descendant is killed; neither is a
// claim about how fast Windows can start a process. Driving them through
// LoadProviderCommand billed the fixture's own startup to the production 5s
// budget, so on a loaded runner the outer timer won the select in
// runProviderCommand: the nonzero-exit test saw a timeout instead of the exit
// status it asserts, and the timer's Terminate killed the background child
// before it recorded its PID, putting the PID file permanently out of reach no
// matter how long the assertion polled for it.
//
// This is a hang-breaker, not a tuned margin. Nothing in a healthy run waits on
// it, since both tests return about one WaitDelay after the fixture exits.
const providerCommandTestBudget = 60 * time.Second

// backgroundChildLifetime must exceed providerCommandTestBudget so that a child
// dying of old age is unobservable: any run where runProviderCommand has not
// returned yet trips the budget first. That is what lets both tests drop their
// old elapsed-versus-lifetime bounds. "Still alive when the call returned" is
// now true by construction, so a child found dead afterwards can only have been
// killed by proc.Terminate.
const backgroundChildLifetime = 120 * time.Second

// TestLoadProviderCommandTerminatesBackgroundChild covers a command that
// exits immediately but leaves a detached child holding the inherited
// stdout/stderr pipes open (e.g. `sleep 600 & exit`). cmd.Wait() only
// unblocks once WaitDelay elapses, and that path must still terminate the
// leftover child instead of just returning and leaking it.
func TestLoadProviderCommandTerminatesBackgroundChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "bg.pid")
	command := writeCommand(t, commandScript{
		Stdout:                 `{"name":"cmd","provider":"openai","apiKey":"sk-command","model":"gpt-command"}`,
		BackgroundSleepSeconds: int(backgroundChildLifetime / time.Second),
		BackgroundPidFile:      pidFile,
	})

	_, _, err := runProviderCommand(command, providerCommandTestBudget)
	if err == nil {
		t.Fatal("runProviderCommand() error = nil, want error from WaitDelay-bounded wait")
	}
	// Name the exact path. The outer-timer path reports the bare sentinel, so
	// matching only that, or the "timed out after 5s" text this test used to
	// match, would also be satisfied by a slow runner taking precisely the path
	// this test exists to rule out.
	if !errors.Is(err, errProviderCommandTimeout) || !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("error = %v, want errProviderCommandTimeout wrapping exec.ErrWaitDelay", err)
	}
	assertProcessTerminated(t, pidFile)
}

// TestLoadProviderCommandTerminatesBackgroundChildOnFailure covers the same
// leaked-descendant scenario as TestLoadProviderCommandTerminatesBackgroundChild,
// but with a shell that exits nonzero. Go's exec.Cmd.Wait only returns
// exec.ErrWaitDelay when the command itself exited successfully; a nonzero
// exit yields the bare *ExitError instead, so the leftover child must still
// be terminated on that path.
func TestLoadProviderCommandTerminatesBackgroundChildOnFailure(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "bg-fail.pid")
	command := writeCommand(t, commandScript{
		Stderr:                 "boom",
		ExitCode:               7,
		BackgroundSleepSeconds: int(backgroundChildLifetime / time.Second),
		BackgroundPidFile:      pidFile,
	})

	_, _, err := runProviderCommand(command, providerCommandTestBudget)
	// The shell's own exit status has to survive. A timeout here means the
	// budget swallowed it, which is exactly what this test kept reporting on CI.
	if errors.Is(err, errProviderCommandTimeout) {
		t.Fatalf("error = %v, want the shell's exit status rather than a timeout", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %v, want *exec.ExitError from the nonzero exit", err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", exitErr.ExitCode())
	}
	assertProcessTerminated(t, pidFile)
}

func TestAssertProcessTerminatedWaitsForDelayedPIDFile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "delayed.pid")
	firstMissing := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		<-firstMissing
		tempFile := pidFile + ".tmp"
		if err := os.WriteFile(tempFile, []byte("2147483647\n"), 0o600); err != nil {
			writeDone <- err
			return
		}
		writeDone <- os.Rename(tempFile, pidFile)
	}()

	missingObserved := false
	assertProcessTerminatedAfterMissing(t, pidFile, func() {
		missingObserved = true
		close(firstMissing)
	})
	if !missingObserved {
		t.Fatal("PID file existed before the missing-file polling path was exercised")
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write delayed pid file: %v", err)
	}
}

func assertProcessTerminatedIfStarted(t *testing.T, pidFile string) {
	t.Helper()

	// The basic timeout fixture is intentionally unsynchronized. On a loaded
	// Windows runner, the cold PowerShell child can be terminated before it
	// records its PID; the synchronized background-child tests below retain
	// the strict PID and liveness checks.
	if _, err := os.Stat(pidFile); errors.Is(err, os.ErrNotExist) {
		return
	}
	assertProcessTerminated(t, pidFile)
}

func assertProcessTerminated(t *testing.T, pidFile string) {
	t.Helper()

	assertProcessTerminatedAfterMissing(t, pidFile, nil)
}

func assertProcessTerminatedAfterMissing(t *testing.T, pidFile string, onMissing func()) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var data []byte
	var err error
	missingObserved := false
	for time.Now().Before(deadline) {
		data, err = os.ReadFile(pidFile)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read sleeper pid file: %v", err)
		}
		if !missingObserved {
			missingObserved = true
			if onMissing != nil {
				onMissing()
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read sleeper pid file before timeout: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse sleeper pid %q: %v", data, err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("sleeper process %d still alive after timeout", pid)
}

func TestLoadProviderCommandInvalidJSON(t *testing.T) {
	command := writeCommand(t, commandScript{Stdout: `{not-json`})

	_, err := LoadProviderCommand(command)
	if err == nil {
		t.Fatal("LoadProviderCommand() error = nil, want JSON error")
	}
	if !strings.Contains(err.Error(), "invalid provider command JSON") {
		t.Fatalf("error = %q, want invalid JSON", err.Error())
	}
}

func TestLoadProviderCommandMissingModel(t *testing.T) {
	command := writeCommand(t, commandScript{
		Stdout: `{"name":"cmd","provider":"openai","apiKey":"sk-command"}`,
	})

	_, err := LoadProviderCommand(command)
	if err == nil {
		t.Fatal("LoadProviderCommand() error = nil, want missing model")
	}
	if !strings.Contains(err.Error(), "provider cmd requires model") {
		t.Fatalf("error = %q, want missing model", err.Error())
	}
}

type commandScript struct {
	Stdout       string
	Stderr       string
	ExitCode     int
	SleepSeconds int
	PidFile      string

	// BackgroundSleepSeconds, if set, spawns a detached child that keeps the
	// inherited stdout/stderr handles open well after the script itself
	// exits, simulating a `sleep 600 & exit` style command. BackgroundPidFile
	// records the detached child's PID.
	BackgroundSleepSeconds int
	BackgroundPidFile      string
}

func writeCommand(t *testing.T, script commandScript) string {
	t.Helper()

	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "provider.cmd")
		lines := []string{"@echo off"}
		if script.SleepSeconds > 0 {
			sleep := "Start-Sleep -Seconds " + itoa(script.SleepSeconds)
			if script.PidFile != "" {
				sleep = "Set-Content -Path '" + psSingleQuote(script.PidFile) + "' -Value $PID -Encoding Ascii; " + sleep
			}
			lines = append(lines, "powershell -NoProfile -Command \""+sleep+"\"")
		}
		if script.Stdout != "" {
			lines = append(lines, "echo "+script.Stdout)
		}
		if script.Stderr != "" {
			lines = append(lines, "echo "+script.Stderr+" 1>&2")
		}
		if script.BackgroundSleepSeconds > 0 {
			readyFile := script.BackgroundPidFile + ".ready"
			bgSleep := "Set-Content -LiteralPath '" + psSingleQuote(script.BackgroundPidFile) + "' -Value $PID -Encoding Ascii; " +
				"Set-Content -LiteralPath '" + psSingleQuote(readyFile) + "' -Value ready -Encoding Ascii; " +
				"Start-Sleep -Seconds " + itoa(script.BackgroundSleepSeconds)
			lines = append(lines, "start /B powershell -NoProfile -Command \""+bgSleep+"\"")
			// Wait for the child to record its PID before exiting, so the PID is
			// on disk while the tree is still alive: runProviderCommand kills the
			// tree one WaitDelay after the script exits, and a child that has not
			// written by then never will.
			//
			// This waits in cmd itself. Spawning a second PowerShell just to poll
			// cost another interpreter cold start, and that start is pure overhead
			// added AFTER the PID is already written. On a loaded runner it pushed
			// the script's exit past the 5s provider-command timeout, so the
			// nonzero-exit test saw a timeout instead of the exit status it
			// asserts. The wait is also bounded now: giving up and letting the PID
			// assertion fail is far more diagnosable than blocking until the
			// timeout fires and reporting an unrelated error. Each iteration is
			// about a second, since `ping -n 2` sleeps between its two echoes,
			// so the ceiling is roughly 15s: far longer than any cold PowerShell
			// start seen on a runner, and far shorter than
			// providerCommandTestBudget, so giving up surfaces a legible
			// PID-file failure rather than a timeout that hides the real cause.
			lines = append(lines,
				":zeroWaitReady",
				"if exist \""+readyFile+"\" goto zeroReady",
				"set /a ZERO_READY_TRIES+=1",
				"if %ZERO_READY_TRIES% GEQ 15 goto zeroReady",
				"ping -n 2 127.0.0.1 >nul",
				"goto zeroWaitReady",
				":zeroReady",
			)
		}
		lines = append(lines, "exit /b "+itoa(script.ExitCode))
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\r\n")), 0o700); err != nil {
			t.Fatalf("write command: %v", err)
		}
		return path
	}

	path := filepath.Join(dir, "provider.sh")
	lines := []string{"#!/bin/sh"}
	if script.SleepSeconds > 0 {
		if script.PidFile != "" {
			lines = append(lines, "echo $$ > '"+shSingleQuote(script.PidFile)+"'")
		}
		lines = append(lines, "sleep "+itoa(script.SleepSeconds))
	}
	if script.Stdout != "" {
		lines = append(lines, "printf '%s\\n' '"+script.Stdout+"'")
	}
	if script.Stderr != "" {
		lines = append(lines, "printf '%s\\n' '"+script.Stderr+"' >&2")
	}
	if script.BackgroundSleepSeconds > 0 {
		lines = append(lines, "sleep "+itoa(script.BackgroundSleepSeconds)+" &")
		lines = append(lines, "echo $! > '"+shSingleQuote(script.BackgroundPidFile)+"'")
	}
	lines = append(lines, "exit "+itoa(script.ExitCode))
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o700); err != nil {
		t.Fatalf("write command: %v", err)
	}
	return path
}

// psSingleQuote escapes a value for interpolation inside a PowerShell
// single-quoted string literal, where a literal quote is doubled.
func psSingleQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// shSingleQuote escapes a value for interpolation inside a POSIX shell
// single-quoted string literal, where a literal quote must close the
// quoted section, emit an escaped quote, then reopen it.
func shSingleQuote(value string) string {
	return strings.ReplaceAll(value, "'", `'\''`)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}

	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
