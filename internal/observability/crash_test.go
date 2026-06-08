package observability

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWriteAndFormatCrashReport(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 6, 8, 10, 30, 0, 0, time.UTC)
	path, err := WriteCrashReport(dir, "cli", "boom", []byte("goroutine 1 [running]:\nmain.x()"), ts)
	if err != nil {
		t.Fatalf("WriteCrashReport: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	report := string(data)
	for _, want := range []string{"boom", "cli", "2026-06-08T10:30:00Z", "goroutine 1"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestRecoverCapturesPanic(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	code := 0

	func() {
		defer Recover(dir, "test", &stderr, &code)
		panic("kaboom")
	}()

	if code != crashExitCode {
		t.Fatalf("exit code = %d, want %d", code, crashExitCode)
	}
	if !strings.Contains(stderr.String(), "zero crashed") || !strings.Contains(stderr.String(), "kaboom") {
		t.Fatalf("missing crash notice: %q", stderr.String())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected one crash report written, got %d", len(entries))
	}
}

func TestRecoverNoPanicIsNoop(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	code := 7
	func() {
		defer Recover(dir, "test", &stderr, &code)
	}()
	if code != 7 {
		t.Fatalf("code changed without a panic: %d", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected output without a panic: %q", stderr.String())
	}
}
