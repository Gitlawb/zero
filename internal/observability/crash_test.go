package observability

import (
	"bytes"
	"errors"
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

func TestWriteCrashReportHardensPreexistingDefaultDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows DACL migration is covered by the daemon integration test")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := DefaultCrashDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".zero"), dir} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := WriteCrashReport(dir, "cli", "boom", []byte("stack"), time.Now()); err != nil {
		t.Fatalf("WriteCrashReport: %v", err)
	}
	for _, path := range []string{filepath.Join(home, ".zero"), dir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got&0o077 != 0 {
			t.Fatalf("directory %s permissions = %04o after migration, want owner-only", path, got)
		}
	}
}

func TestWriteCrashReportBindsDirectoryDuringSwap(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "live")
	movedDir := filepath.Join(parent, "moved")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	var swapErr error
	path, err := writeCrashReport(dir, "cli", "secret panic", []byte("secret stack"), ts, crashReportHooks{
		beforePublish: func() {
			swapErr = os.Rename(dir, movedDir)
			if swapErr != nil {
				return
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatalf("create substitute crash directory: %v", err)
			}
		},
	})
	if err != nil {
		t.Fatalf("writeCrashReport: %v", err)
	}
	if swapErr != nil {
		if runtime.GOOS != "windows" {
			t.Fatalf("swap crash directory: %v", swapErr)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("report missing after Windows blocked directory swap: %v", err)
		}
		return
	}

	if path != "" {
		t.Fatalf("writeCrashReport returned stale path %q after directory swap", path)
	}
	reportName := "crash-" + ts.UTC().Format("20060102-150405") + ".log"
	data, err := os.ReadFile(filepath.Join(movedDir, reportName))
	if err != nil {
		t.Fatalf("read report through originally bound directory: %v", err)
	}
	if !strings.Contains(string(data), "secret panic") {
		t.Fatalf("report in bound directory missing panic: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, reportName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("substitute directory received crash report: %v", err)
	}
}

func TestWriteCrashReportPublishesOnlyCompleteContent(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	finalPath := filepath.Join(dir, "crash-20260824-120000.log")
	staged := make(chan struct{})
	release := make(chan struct{})
	result := make(chan crashWriteResult, 1)
	go func() {
		path, err := writeCrashReport(dir, "cli", "boom", []byte("complete stack"), ts, crashReportHooks{
			beforePublish: func() {
				close(staged)
				<-release
			},
		})
		result <- crashWriteResult{path: path, err: err}
	}()
	select {
	case <-staged:
	case written := <-result:
		t.Fatalf("writeCrashReport returned before publication hook: %v", written.err)
	case <-time.After(time.Second):
		t.Fatal("writeCrashReport did not reach publication hook")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if _, err := os.Stat(finalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final report visible before complete publication: %v", err)
	}
	close(release)
	written := <-result
	if written.err != nil {
		t.Fatalf("writeCrashReport: %v", written.err)
	}
	data, err := os.ReadFile(written.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "complete stack") {
		t.Fatalf("published report is incomplete: %q", data)
	}
}

func TestWriteCrashReportRemovesTempAfterWriteFailure(t *testing.T) {
	dir := t.TempDir()
	injected := errors.New("injected write failure")
	_, err := writeCrashReport(dir, "cli", "boom", []byte("stack"), time.Now(), crashReportHooks{
		write: func(*os.File, []byte) (int, error) { return 0, injected },
	})
	if !errors.Is(err, injected) {
		t.Fatalf("writeCrashReport error = %v, want injected failure", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("crash directory contains partial files after failure: %v", entries)
	}
}

func TestWriteCrashReportDoesNotOverwriteExistingReport(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "crash-20260824-120000.log")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteCrashReport(dir, "cli", "boom", []byte("stack"), ts); !errors.Is(err, os.ErrExist) {
		t.Fatalf("WriteCrashReport error = %v, want existing destination", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Fatalf("existing report overwritten: %q", data)
	}
}

type crashWriteResult struct {
	path string
	err  error
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
