package observability

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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

func TestWriteCrashReportCreatesPrivateDefaultDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := DefaultCrashDir()
	if dir != filepath.Join(home, ".zero", "crashes") {
		t.Fatalf("DefaultCrashDir = %q, want path beneath temporary home", dir)
	}
	if _, err := WriteCrashReport(dir, "cli", "boom", []byte("stack"), time.Now()); err != nil {
		t.Fatalf("WriteCrashReport: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, path := range []string{filepath.Join(home, ".zero"), dir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got&0o077 != 0 {
			t.Fatalf("directory %s permissions = %04o, want owner-only", path, got)
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
