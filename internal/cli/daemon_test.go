package cli

import (
	"bytes"
	"strings"
	"testing"
)

// isolateDaemonPaths points DefaultPaths at a temp dir so the test never touches
// a real daemon on the dev machine.
func isolateDaemonPaths(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
}

func runDaemonCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runDaemon(args, &out, &errb, appDeps{})
	return code, out.String(), errb.String()
}

func TestDaemonUsage(t *testing.T) {
	code, _, _ := runDaemonCLI(t)
	if code != exitUsage {
		t.Fatalf("no-args exit = %d, want exitUsage", code)
	}
	code, out, _ := runDaemonCLI(t, "--help")
	if code != exitSuccess || !strings.Contains(out, "Usage: zero daemon") {
		t.Fatalf("--help exit=%d out=%q", code, out)
	}
}

func TestDaemonUnknownSubcommand(t *testing.T) {
	code, _, errb := runDaemonCLI(t, "frobnicate")
	if code != exitUsage {
		t.Fatalf("unknown subcommand exit = %d, want exitUsage", code)
	}
	if !strings.Contains(errb, "unknown daemon subcommand") {
		t.Fatalf("stderr = %q, want unknown-subcommand message", errb)
	}
}

func TestDaemonRunRequiresSession(t *testing.T) {
	isolateDaemonPaths(t)
	code, _, errb := runDaemonCLI(t, "run", "--prompt", "hi")
	if code != exitUsage {
		t.Fatalf("run without --session exit = %d, want exitUsage", code)
	}
	if !strings.Contains(errb, "--session") {
		t.Fatalf("stderr = %q, want a --session hint", errb)
	}
}

func TestDaemonRunRequiresPromptOrArgs(t *testing.T) {
	isolateDaemonPaths(t)
	code, _, errb := runDaemonCLI(t, "run", "--session", "s1")
	if code != exitUsage {
		t.Fatalf("run without prompt/args exit = %d, want exitUsage", code)
	}
	if !strings.Contains(errb, "--prompt") {
		t.Fatalf("stderr = %q, want a --prompt hint", errb)
	}
}

func TestDaemonStopWhenNotRunning(t *testing.T) {
	isolateDaemonPaths(t)
	code, out, _ := runDaemonCLI(t, "stop")
	if code != exitSuccess {
		t.Fatalf("stop (not running) exit = %d, want exitSuccess", code)
	}
	if !strings.Contains(out, "not running") {
		t.Fatalf("stop output = %q, want 'not running'", out)
	}
}

func TestDaemonStatusWhenNotRunning(t *testing.T) {
	isolateDaemonPaths(t)
	code, out, _ := runDaemonCLI(t, "status")
	if code != exitSuccess {
		t.Fatalf("status (not running) exit = %d, want exitSuccess", code)
	}
	if !strings.Contains(out, "not running") {
		t.Fatalf("status output = %q, want 'not running'", out)
	}
}

func TestDaemonAttachRequiresSession(t *testing.T) {
	isolateDaemonPaths(t)
	code, _, errb := runDaemonCLI(t, "attach")
	if code != exitUsage {
		t.Fatalf("attach without session exit = %d, want exitUsage", code)
	}
	if !strings.Contains(errb, "session") {
		t.Fatalf("stderr = %q, want a session hint", errb)
	}
}

func TestDaemonRunWhenNotRunning(t *testing.T) {
	isolateDaemonPaths(t)
	code, _, errb := runDaemonCLI(t, "run", "--session", "s1", "--prompt", "hello")
	if code != exitCrash {
		t.Fatalf("run (no daemon) exit = %d, want exitCrash", code)
	}
	if !strings.Contains(errb, "not running") {
		t.Fatalf("stderr = %q, want 'not running'", errb)
	}
}

func TestDaemonSubcommandsRejectExtraArgs(t *testing.T) {
	isolateDaemonPaths(t)
	cases := [][]string{
		{"stop", "oops"},
		{"status", "oops"},
		{"attach", "s1", "extra"},
	}
	for _, args := range cases {
		code, _, errb := runDaemonCLI(t, args...)
		if code != exitUsage {
			t.Fatalf("%v exit = %d, want exitUsage (reject extra args); stderr=%q", args, code, errb)
		}
	}
}
