// Package observability provides dependency-free crash capture for the CLI: a
// recovered panic is written to a local crash report (timestamp, label, stack)
// and surfaced to the user as a brief notice instead of a raw stack trace. It is
// the fail-open foundation for remote crash/metrics reporting — a Sentry/OTEL
// adapter can hook the same Recover/report path when configured — without
// pulling those dependencies into the base build.
package observability

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// crashExitCode is returned when a top-level panic is recovered.
const crashExitCode = 1

// FormatCrashReport renders a human-readable crash report.
func FormatCrashReport(label string, recovered any, stack []byte, ts time.Time) string {
	return fmt.Sprintf("zero crash report\ntime:  %s\nlabel: %s\npanic: %v\n\nstack:\n%s\n",
		ts.UTC().Format(time.RFC3339), label, recovered, stack)
}

// WriteCrashReport writes a crash report file into dir and returns its path.
func WriteCrashReport(dir, label string, recovered any, stack []byte, ts time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "crash-"+ts.UTC().Format("20060102-150405")+".log")
	if err := os.WriteFile(path, []byte(FormatCrashReport(label, recovered, stack, ts)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// DefaultCrashDir is where crash reports are written by default.
func DefaultCrashDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".zero", "crashes")
	}
	return filepath.Join(os.TempDir(), "zero-crashes")
}

// Recover is deferred at a top-level entrypoint. On a panic it captures the
// stack, writes a crash report under dir, prints a brief notice to stderr, and
// sets *code to a crash exit code. It is fail-open: if the report can't be
// written it still reports the crash with the stack inline. No panic escapes.
func Recover(dir, label string, stderr io.Writer, code *int) {
	recovered := recover()
	if recovered == nil {
		return
	}
	stack := debug.Stack()
	if path, err := WriteCrashReport(dir, label, recovered, stack, time.Now()); err == nil {
		fmt.Fprintf(stderr, "zero crashed: %v\nA crash report was saved to %s\n", recovered, path)
	} else {
		fmt.Fprintf(stderr, "zero crashed: %v\n%s\n", recovered, stack)
	}
	*code = crashExitCode
}
