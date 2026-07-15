package config

import (
	"os"
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
	assertProcessTerminated(t, pidFile)
}

// TestLoadProviderCommandTerminatesBackgroundChild covers a command that
// exits immediately but leaves a detached child holding the inherited
// stdout/stderr pipes open (e.g. `sleep 600 & exit`). cmd.Wait() only
// unblocks once WaitDelay elapses, and that path must still terminate the
// leftover child instead of just returning and leaking it.
func TestLoadProviderCommandTerminatesBackgroundChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "bg.pid")
	command := writeCommand(t, commandScript{
		Stdout:                 `{"name":"cmd","provider":"openai","apiKey":"sk-command","model":"gpt-command"}`,
		BackgroundSleepSeconds: 10,
		BackgroundPidFile:      pidFile,
	})

	start := time.Now()
	_, err := LoadProviderCommand(command)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("LoadProviderCommand() error = nil, want error from WaitDelay-bounded wait")
	}
	if !strings.Contains(err.Error(), "timed out after 5s") {
		t.Fatalf("error = %q, want timeout", err.Error())
	}
	if elapsed > 4*time.Second {
		t.Fatalf("returned after %s, want well under the 5s provider-command timeout since WaitDelay (1s) should trigger termination", elapsed)
	}
	assertProcessTerminated(t, pidFile)
}

func assertProcessTerminated(t *testing.T, pidFile string) {
	t.Helper()

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read sleeper pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse sleeper pid %q: %v", data, err)
	}

	deadline := time.Now().Add(3 * time.Second)
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
			bgSleep := "Set-Content -Path '" + psSingleQuote(script.BackgroundPidFile) + "' -Value $PID -Encoding Ascii; Start-Sleep -Seconds " + itoa(script.BackgroundSleepSeconds)
			lines = append(lines, "start /B powershell -NoProfile -Command \""+bgSleep+"\"")
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
// single-quoted string literal, where a literal quote is written as ''.
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
