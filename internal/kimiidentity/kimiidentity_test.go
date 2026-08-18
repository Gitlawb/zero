package kimiidentity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeadersIncludesDeviceIdentity(t *testing.T) {
	IsolateDeviceIDStorage(t)
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

// TestDeviceIDReloadsWhenConfigRootChanges pins the path-keyed cache: after an
// identity is cached for one config root, redirecting os.UserConfigDir must
// mint (or load) a different root's id rather than returning the first.
func TestDeviceIDReloadsWhenConfigRootChanges(t *testing.T) {
	root1 := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root1)
	t.Setenv("APPDATA", root1)
	t.Setenv("HOME", root1)
	id1 := DeviceID()
	if !isUUID(id1) {
		t.Fatalf("DeviceID() under root1 = %q, want UUID", id1)
	}
	path1 := mustDeviceIDPath(t)
	if raw, err := os.ReadFile(path1); err != nil {
		t.Fatalf("read root1 device id: %v", err)
	} else if got := strings.TrimSpace(string(raw)); got != id1 {
		t.Fatalf("root1 file = %q, want %q", got, id1)
	}

	root2 := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root2)
	t.Setenv("APPDATA", root2)
	t.Setenv("HOME", root2)
	id2 := DeviceID()
	if !isUUID(id2) {
		t.Fatalf("DeviceID() under root2 = %q, want UUID", id2)
	}
	if id1 == id2 {
		t.Fatalf("DeviceID reused first root's id %q after config root change", id1)
	}
	path2 := mustDeviceIDPath(t)
	if path1 == path2 {
		t.Fatalf("device id path did not change with config root: %q", path1)
	}
	if raw, err := os.ReadFile(path2); err != nil {
		t.Fatalf("read root2 device id: %v", err)
	} else if got := strings.TrimSpace(string(raw)); got != id2 {
		t.Fatalf("root2 file = %q, want %q", got, id2)
	}
	// First root's file must still hold id1 (no clobber across roots).
	if raw, err := os.ReadFile(path1); err != nil {
		t.Fatalf("re-read root1 device id: %v", err)
	} else if got := strings.TrimSpace(string(raw)); got != id1 {
		t.Fatalf("root1 file changed after root2 mint: got %q, want %q", got, id1)
	}
}

func mustDeviceIDPath(t *testing.T) string {
	t.Helper()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	return filepath.Join(configDir, "zero", "kimi-device-id")
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

// TestLoadOrCreateDeviceIDConvergesWhenLockReadIsTransient is the Windows
// exclusive-create regression: a holder deleting the lock after publish can
// make a concurrent ReadFile return a sharing-violation (not ErrNotExist).
// That must be treated as contention so every caller adopts the persisted id.
func TestLoadOrCreateDeviceIDConvergesWhenLockReadIsTransient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	pause := make(chan struct{})
	paused := make(chan struct{})
	var once sync.Once
	cleanupHook := SetBeforeRenameHook(func() {
		once.Do(func() {
			close(paused)
			<-pause
		})
	})
	defer cleanupHook()

	var injected atomic.Bool
	transientReadErr := errors.New("sharing violation")
	cleanupRead := SetReadDeviceLock(func(root *os.Root, name string) ([]byte, error) {
		if injected.CompareAndSwap(false, true) {
			return nil, transientReadErr
		}
		return root.ReadFile(name)
	})
	defer cleanupRead()

	var publisherID string
	var pubWg sync.WaitGroup
	pubWg.Add(1)
	go func() {
		defer pubWg.Done()
		publisherID = loadOrCreateDeviceIDAt(path)
	}()
	<-paused

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
	// Let racers observe the lock (and the injected read failure) first.
	time.Sleep(30 * time.Millisecond)
	close(pause)
	pubWg.Wait()
	wg.Wait()

	if !isUUID(publisherID) {
		t.Fatalf("publisher id %q is not a UUID", publisherID)
	}
	for i, id := range ids {
		if id != publisherID {
			t.Fatalf("worker %d returned %q, want publisher %q (all: %v)", i, id, publisherID, ids)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted id: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != publisherID {
		t.Fatalf("persisted %q, want publisher %q", persisted, publisherID)
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
// multi-process window where the lock winner has acquired the lock but not
// yet published the UUID. Concurrent callers must wait and adopt that UUID
// rather than each minting a divergent identity.
func TestLoadOrCreateDeviceIDAdoptsWinnerAfterEmptyCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	// Simulate the lock winner holding the lock before writing.
	lockF, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	ownerToken := fmt.Sprintf("%d.%d\n", os.Getpid(), time.Now().UnixNano())
	_, _ = lockF.WriteString(ownerToken)
	_ = lockF.Sync()

	const winner = "11111111-2222-4333-8444-555555555555"
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(path, []byte(winner+"\n"), 0o600)
		_ = lockF.Close()
		_ = os.Remove(lockPath)
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
	// Empty lock contents are treated as abandoned (unparseable holder) and
	// reclaimed. A live holder's "<pid>.<nano>" token is not reclaimed.
	lockF, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = lockF.Close()

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
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("repair lock should be cleaned up after reclaim+repair: err=%v", err)
	}
}

func TestLoadOrCreateDeviceIDRepairsDeadPIDRepairLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	lockPath := path + ".lock"
	// Well-formed token with a non-positive PID is treated as dead (same as a
	// crashed holder). Avoid inventing a high PID that might be live.
	if err := os.WriteFile(lockPath, []byte("0.12345\n"), 0o600); err != nil {
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

func TestLockHolderAlive(t *testing.T) {
	if lockHolderAlive([]byte("")) {
		t.Fatal("empty lock should not be treated as live")
	}
	if lockHolderAlive([]byte("not-a-token")) {
		t.Fatal("unparseable lock should not be treated as live")
	}
	if lockHolderAlive([]byte("0.1")) {
		t.Fatal("non-positive pid should not be treated as live")
	}
	// Our own PID must be treated as live so we never reclaim our own lock.
	self := fmt.Sprintf("%d.%d", os.Getpid(), time.Now().UnixNano())
	if !lockHolderAlive([]byte(self)) {
		t.Fatalf("self pid token %q should be live", self)
	}
	// A live PID with an expired timestamp is dead: PID reuse must not pin the lock.
	stale := fmt.Sprintf("%d.%d", os.Getpid(), time.Now().Add(-time.Hour).UnixNano())
	if lockHolderAlive([]byte(stale)) {
		t.Fatalf("expired lease %q should not be live", stale)
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

// TestLoadOrCreateDeviceIDPausedPublisherConverges covers a publisher that is
// paused/descheduled before its rename: concurrent callers must wait for the
// lock holder to complete publication and adopt its published ID rather than
// treating it as abandoned and overwriting it.
func TestLoadOrCreateDeviceIDPausedPublisherConverges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	pause := make(chan struct{})
	paused := make(chan struct{})
	var once sync.Once
	cleanup := SetBeforeRenameHook(func() {
		once.Do(func() {
			close(paused)
			<-pause
		})
	})
	defer cleanup()

	var publisherID string
	var pubWg sync.WaitGroup
	pubWg.Add(1)
	go func() {
		defer pubWg.Done()
		publisherID = loadOrCreateDeviceIDAt(path)
	}()

	// Wait until the publisher holds the lock and reaches the hook before rename.
	<-paused

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

	// Give racers a chance to observe the lock, then resume the publisher.
	time.Sleep(30 * time.Millisecond)
	close(pause)

	pubWg.Wait()
	wg.Wait()

	if !isUUID(publisherID) {
		t.Fatalf("publisher id %q is not a UUID", publisherID)
	}
	for i, id := range ids {
		if id != publisherID {
			t.Fatalf("worker %d returned %q, want publisher winner %q", i, id, publisherID)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted id: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != publisherID {
		t.Fatalf("persisted %q, want publisher winner %q", persisted, publisherID)
	}
}

// TestLoadOrCreateDeviceIDWaitsForSlowLiveRepairLockHolder ensures that when a live
// process owns the repair lock, a competing caller waits for the published ID
// instead of returning an unpersisted locally-generated ID.
func TestLoadOrCreateDeviceIDWaitsForSlowLiveRepairLockHolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	lockPath := path + ".lock"
	// Live owner token using our own PID.
	ownerToken := fmt.Sprintf("%d.%d\n", os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(lockPath, []byte(ownerToken), 0o600); err != nil {
		t.Fatal(err)
	}

	const publishedID = "33333333-4444-4555-8666-777777777777"
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Hold the lock longer than a quick retry window.
		time.Sleep(100 * time.Millisecond)
		if err := os.WriteFile(path, []byte(publishedID+"\n"), 0o600); err != nil {
			t.Errorf("write published id: %v", err)
		}
		_ = os.Remove(lockPath)
	}()

	got := loadOrCreateDeviceIDAt(path)
	<-done

	if got != publishedID {
		t.Fatalf("loadOrCreateDeviceIDAt = %q, want published %q", got, publishedID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted device ID: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != publishedID {
		t.Fatalf("persisted device ID = %q, want %q", persisted, publishedID)
	}
}

// TestLiveLockHolderLeaseNeverOverwrittenByCompetitor pins that while a live lock
// holder owns the lease, competing callers must never publish or overwrite the
// device ID file without holding the lock.
func TestLiveLockHolderLeaseNeverOverwrittenByCompetitor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	lockPath := path + ".lock"
	ownerToken := fmt.Sprintf("%d.%d\n", os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(lockPath, []byte(ownerToken), 0o600); err != nil {
		t.Fatal(err)
	}

	const holderPublishedID = "11111111-2222-4333-8444-555555555555"
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(150 * time.Millisecond)
		if err := os.WriteFile(path, []byte(holderPublishedID+"\n"), 0o600); err != nil {
			t.Errorf("write published ID: %v", err)
		}
		_ = os.Remove(lockPath)
	}()

	got := loadOrCreateDeviceIDAt(path)
	<-done

	if got != holderPublishedID {
		t.Fatalf("competitor returned %q, want holder's %q", got, holderPublishedID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != holderPublishedID {
		t.Fatalf("persisted file = %q, want holder's %q", persisted, holderPublishedID)
	}
}

func TestLoadOrCreateDeviceIDUnwritableDirReturnsLocalWithoutHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write-bit is not enforced the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write a 0555 directory")
	}
	dir := t.TempDir()
	zeroDir := filepath.Join(dir, "zero")
	if err := os.MkdirAll(zeroDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(zeroDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(zeroDir, 0o700) })
	path := filepath.Join(zeroDir, "kimi-device-id")

	done := make(chan string, 1)
	go func() { done <- loadOrCreateDeviceIDAt(path) }()
	var got string
	select {
	case got = <-done:
	case <-time.After(time.Second):
		t.Fatal("identity acquisition hung on unwritable storage")
	}
	if !isUUID(got) {
		t.Fatalf("process-local id %q is not a UUID", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unwritable storage must not persist a device id: stat err=%v", err)
	}
}

func TestLoadOrCreateDeviceIDReclaimsExpiredLivePIDLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	// Our PID is live, but the timestamp is older than the lease TTL: this is
	// the PID-reuse case, not a current holder.
	stale := fmt.Sprintf("%d.%d\n", os.Getpid(), time.Now().Add(-time.Hour).UnixNano())
	if err := os.WriteFile(lockPath, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	got := loadOrCreateDeviceIDAt(path)
	if !isUUID(got) {
		t.Fatalf("reclaimed id %q is not a UUID", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted id: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != got {
		t.Fatalf("persisted %q, want reclaimed %q", persisted, got)
	}
}

func TestLoadOrCreateDeviceIDLiveHolderPastDeadlineDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const holderID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if err := os.WriteFile(path, []byte(holderID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	ownerToken := fmt.Sprintf("%d.%d\n", os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(lockPath, []byte(ownerToken), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := SetDeviceIDMaxWait(0)
	defer restore()

	got := loadOrCreateDeviceIDAt(path)
	if got != holderID {
		t.Fatalf("got %q, want existing holder id %q", got, holderID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted id: %v", err)
	}
	if persisted := strings.TrimSpace(string(raw)); persisted != holderID {
		t.Fatalf("persisted %q, want unchanged holder id %q", persisted, holderID)
	}
}

func TestLoadOrCreateDeviceIDLiveHolderPastDeadlineLeavesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	ownerToken := fmt.Sprintf("%d.%d\n", os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(lockPath, []byte(ownerToken), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := SetDeviceIDMaxWait(0)
	defer restore()

	got := loadOrCreateDeviceIDAt(path)
	if !isUUID(got) {
		t.Fatalf("process-local id %q is not a UUID", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live-holder deadline fallback must not publish: stat err=%v", err)
	}
}

func TestLoadOrCreateDeviceIDCancelReturnsLocalWithoutWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	ownerToken := fmt.Sprintf("%d.%d\n", os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(lockPath, []byte(ownerToken), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := LoadOrCreateDeviceIDAtContext(ctx, path)
	if !isUUID(got) {
		t.Fatalf("canceled id %q is not a UUID", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancellation must not persist a device id: stat err=%v", err)
	}
}
