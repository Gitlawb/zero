package config

import (
	"os"
	"path/filepath"
	"runtime"
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
	command := writeCommand(t, commandScript{SleepSeconds: 10})

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
}

func writeCommand(t *testing.T, script commandScript) string {
	t.Helper()

	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "provider.cmd")
		lines := []string{"@echo off"}
		if script.SleepSeconds > 0 {
			lines = append(lines, "powershell -NoProfile -Command \"Start-Sleep -Seconds "+itoa(script.SleepSeconds)+"\"")
		}
		if script.Stdout != "" {
			lines = append(lines, "echo "+script.Stdout)
		}
		if script.Stderr != "" {
			lines = append(lines, "echo "+script.Stderr+" 1>&2")
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
		lines = append(lines, "sleep "+itoa(script.SleepSeconds))
	}
	if script.Stdout != "" {
		lines = append(lines, "printf '%s\\n' '"+script.Stdout+"'")
	}
	if script.Stderr != "" {
		lines = append(lines, "printf '%s\\n' '"+script.Stderr+"' >&2")
	}
	lines = append(lines, "exit "+itoa(script.ExitCode))
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o700); err != nil {
		t.Fatalf("write command: %v", err)
	}
	return path
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
