package kimiidentity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// setUserConfigRoot redirects os.UserConfigDir() to a throwaway temp dir so a
// test that calls the process-global Headers()/DeviceID() never creates or
// touches the real ~/.config/zero (or AppData\zero) kimi-device-id file.
// Mirrors internal/workspacetrust/trust_test.go: os.UserConfigDir reads
// APPDATA on Windows, HOME on darwin, and XDG_CONFIG_HOME on Linux, so a
// single env var isn't portable. Must be called before the first call to
// Headers()/DeviceID() in the process — DeviceID is a sync.OnceValue, so a
// call before the redirect is in place would permanently cache the real path.
func setUserConfigRoot(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	ResetDeviceIDForTest()
	t.Cleanup(ResetDeviceIDForTest)
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", root)
	case "darwin":
		t.Setenv("HOME", root)
	default:
		t.Setenv("XDG_CONFIG_HOME", root)
	}
}

func TestHeadersIncludesDeviceIdentity(t *testing.T) {
	setUserConfigRoot(t)
	headers := Headers()
	for _, key := range []string{
		"X-Msh-Platform",
		"X-Msh-Version",
		"X-Msh-Device-Name",
		"X-Msh-Device-Model",
		"X-Msh-Os-Version",
		"X-Msh-Device-Id",
	} {
		if headers[key] == "" {
			t.Fatalf("Headers()[%q] empty", key)
		}
	}
	if headers["X-Msh-Platform"] != "kimi_code_cli" {
		t.Fatalf("X-Msh-Platform = %q, want kimi_code_cli", headers["X-Msh-Platform"])
	}
	if !isUUID(headers["X-Msh-Device-Id"]) {
		t.Fatalf("X-Msh-Device-Id = %q, want UUID", headers["X-Msh-Device-Id"])
	}
}

func TestLoadOrCreateDeviceIDExclusiveCreate(t *testing.T) {
	// Exercise the production loader directly via its path-parameterized
	// helper. Concurrent first-use must converge on a single persisted ID:
	// the O_EXCL loser reads back the winner's file instead of overwriting it.
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	ids := make([]string, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(i int) {
			defer wg.Done()
			ids[i] = loadOrCreateDeviceIDAt(path)
		}(i)
	}
	wg.Wait()

	winner := ""
	for _, id := range ids {
		if id == "" {
			t.Fatal("worker returned empty id")
		}
		if winner == "" {
			winner = id
			continue
		}
		if id != winner {
			t.Fatalf("workers diverged: got %q and %q", winner, id)
		}
	}
	if !isUUID(winner) {
		t.Fatalf("winner id %q is not a UUID", winner)
	}
	// The persisted file carries the winner exactly once.
	if raw, err := os.ReadFile(path); err != nil {
		t.Fatalf("read persisted id: %v", err)
	} else if got := strings.TrimSpace(string(raw)); got != winner {
		t.Fatalf("persisted id = %q, want %q", got, winner)
	}
}

func TestLoadOrCreateDeviceIDReadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const existing = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if err := os.WriteFile(path, []byte(existing+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadOrCreateDeviceIDAt(path); got != existing {
		t.Fatalf("loadOrCreateDeviceIDAt = %q, want existing %q", got, existing)
	}
}

// TestLoadOrCreateDeviceIDAdoptsWinnerAfterEmptyCreate covers the
// multi-process window where the O_EXCL winner has created the file but not
// yet written the UUID. Concurrent callers must wait and adopt that UUID
// rather than each minting a divergent identity.
func TestLoadOrCreateDeviceIDAdoptsWinnerAfterEmptyCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate the exclusive-create winner that has not written yet.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	const winner = "11111111-2222-4333-8444-555555555555"
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(30 * time.Millisecond)
		_, _ = f.WriteString(winner + "\n")
		_ = f.Sync()
		_ = f.Close()
	}()

	const workers = 4
	ids := make([]string, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(i int) {
			defer wg.Done()
			ids[i] = loadOrCreateDeviceIDAt(path)
		}(i)
	}
	wg.Wait()
	<-done

	for _, id := range ids {
		if id != winner {
			t.Fatalf("worker returned %q, want winner %q (all: %v)", id, winner, ids)
		}
	}
}

// TestLoadOrCreateDeviceIDRepairsAbandonedEmptyFile covers the case where
// a previous process exclusive-created the path and died before writing a
// UUID. Callers must not permanently diverge: after the retry window the
// empty file is removed and a new exclusive create publishes a valid id.
func TestLoadOrCreateDeviceIDRepairsAbandonedEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close() // abandoned: never written

	got := loadOrCreateDeviceIDAt(path)
	if !isUUID(got) {
		t.Fatalf("repaired id %q is not a UUID", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repaired file: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != got {
		t.Fatalf("persisted %q, want repaired %q", persisted, got)
	}
}

// TestLoadOrCreateDeviceIDConcurrentAbandonedFileRepairConverges covers
// multiple racing processes all finding the same abandoned/invalid file at
// once. Repair must be mutually exclusive: only one racer may remove and
// recreate the file, so every caller ends up with the same id and that id is
// exactly what is persisted (no caller returns an in-memory id that a later
// repair silently unlinked and replaced).
func TestLoadOrCreateDeviceIDConcurrentAbandonedFileRepairConverges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close() // abandoned: never written

	const workers = 16
	ids := make([]string, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(i int) {
			defer wg.Done()
			ids[i] = loadOrCreateDeviceIDAt(path)
		}(i)
	}
	wg.Wait()

	winner := ""
	for _, id := range ids {
		if id == "" {
			t.Fatal("worker returned empty id")
		}
		if winner == "" {
			winner = id
			continue
		}
		if id != winner {
			t.Fatalf("workers diverged repairing abandoned file: got %q and %q", winner, id)
		}
	}
	if !isUUID(winner) {
		t.Fatalf("winner id %q is not a UUID", winner)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted id: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != winner {
		t.Fatalf("persisted %q, want winner %q", persisted, winner)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("repair lock file left behind: err=%v", err)
	}
}

func TestLoadOrCreateDeviceIDRepairsStaleRepairLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close() // abandoned target file

	lockPath := path + ".lock"
	lockF, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = lockF.Close() // abandoned lock file
	// Backdate lock file mtime to make it stale (> 1s)
	oldTime := time.Now().Add(-5 * time.Second)
	if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	got := loadOrCreateDeviceIDAt(path)
	if !isUUID(got) {
		t.Fatalf("repaired id %q is not a UUID", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repaired file: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != got {
		t.Fatalf("persisted %q, want repaired %q", persisted, got)
	}
}

func TestAsciiHeaderValueStripsNonPrintable(t *testing.T) {
	if got := asciiHeaderValue("linux#6.1"); got != "linux#6.1" {
		// printable ASCII including # is kept; the kimi-cli bug was a different
		// control character path — ensure we still strip true controls.
		t.Fatalf("got %q", got)
	}
	if got := asciiHeaderValue("a\nb\x00c"); got != "abc" {
		t.Fatalf("got %q, want abc", got)
	}
	if got := asciiHeaderValue("\x01\x02"); got != "unknown" {
		t.Fatalf("got %q, want unknown", got)
	}
}
