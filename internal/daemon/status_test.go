package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatusFileAtomicPublicationConcurrentReaders(t *testing.T) {
	launcher, _ := seqLauncher(&fakeWorker{pid: 1})
	srv, paths := newTestServer(t, launcher)
	srv.startedAt = time.Now().UTC().Truncate(time.Millisecond)

	// Publish initial status
	if err := srv.writeStatusFile(); err != nil {
		t.Fatalf("initial writeStatusFile: %v", err)
	}

	var stop atomic.Bool
	var readerErrors atomic.Int64
	var readCount atomic.Int64
	var wg sync.WaitGroup

	numReaders := 8
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				data, err := os.ReadFile(paths.Status)
				if err != nil {
					// Transient Windows absent window or retryable read
					continue
				}
				if len(data) == 0 {
					readerErrors.Add(1)
					t.Errorf("observed empty status file during concurrent read")
					return
				}
				var sf StatusFile
				if err := json.Unmarshal(data, &sf); err != nil {
					readerErrors.Add(1)
					t.Errorf("observed partial or corrupted status JSON: %v (raw: %q)", err, string(data))
					return
				}
				if sf.PID != os.Getpid() {
					readerErrors.Add(1)
					t.Errorf("invalid PID in status file: got %d, want %d", sf.PID, os.Getpid())
					return
				}
				if sf.Socket != paths.Socket {
					readerErrors.Add(1)
					t.Errorf("invalid socket in status file: got %q, want %q", sf.Socket, paths.Socket)
					return
				}
				if sf.Version < 1 {
					readerErrors.Add(1)
					t.Errorf("invalid version in status file: %d", sf.Version)
					return
				}
				readCount.Add(1)
			}
		}()
	}

	// Repeatedly update status file to stress concurrent reader/writer synchronization
	numUpdates := 150
	for i := 1; i <= numUpdates; i++ {
		srv.opts.Version = i
		srv.startedAt = time.Now().UTC().Add(time.Duration(i) * time.Second).Truncate(time.Millisecond)
		if err := srv.writeStatusFile(); err != nil {
			t.Fatalf("writeStatusFile iteration %d: %v", i, err)
		}
	}

	stop.Store(true)
	wg.Wait()

	if readerErrors.Load() > 0 {
		t.Fatalf("%d reader errors detected during atomic status publication", readerErrors.Load())
	}
	if readCount.Load() == 0 {
		t.Fatal("no successful concurrent reads completed")
	}
}

func TestStatusFileFaultInjectionPreservesExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions-based fault injection skipped on Windows")
	}

	launcher, _ := seqLauncher(&fakeWorker{pid: 1})
	srv, paths := newTestServer(t, launcher)

	initialStartedAt := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	srv.opts.Version = 10
	srv.startedAt = initialStartedAt

	// 1. Initial successful status publication
	if err := srv.writeStatusFile(); err != nil {
		t.Fatalf("initial writeStatusFile: %v", err)
	}

	initialData, err := os.ReadFile(paths.Status)
	if err != nil {
		t.Fatalf("ReadFile initial status: %v", err)
	}
	var initialStatus StatusFile
	if err := json.Unmarshal(initialData, &initialStatus); err != nil {
		t.Fatalf("Unmarshal initial status: %v", err)
	}
	if initialStatus.Version != 10 {
		t.Fatalf("initial version = %d, want 10", initialStatus.Version)
	}

	// 2. Inject fault: make parent directory read-only so sibling temp file creation fails
	dir := filepath.Dir(paths.Status)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod dir to 0500: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()

	// 3. Attempt update with new version, which must fail
	srv.opts.Version = 99
	srv.startedAt = time.Now().UTC()
	err = srv.writeStatusFile()
	if err == nil {
		t.Fatal("expected writeStatusFile to fail on read-only directory")
	}

	// 4. Restore directory permissions
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restore Chmod dir: %v", err)
	}

	// 5. Verify the old status document survived unharmed and was not truncated
	survivingData, err := os.ReadFile(paths.Status)
	if err != nil {
		t.Fatalf("ReadFile surviving status: %v", err)
	}
	if len(survivingData) == 0 {
		t.Fatal("status file was truncated in place during failed write")
	}

	var survivingStatus StatusFile
	if err := json.Unmarshal(survivingData, &survivingStatus); err != nil {
		t.Fatalf("surviving status file is corrupted: %v (raw: %q)", err, string(survivingData))
	}
	if survivingStatus.Version != 10 {
		t.Fatalf("surviving version = %d, want 10 (old document should be preserved)", survivingStatus.Version)
	}
	if !survivingStatus.StartedAt.Equal(initialStartedAt) {
		t.Fatalf("surviving startedAt = %v, want %v", survivingStatus.StartedAt, initialStartedAt)
	}
}

func TestStatusFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permission checks skipped on Windows")
	}

	launcher, _ := seqLauncher(&fakeWorker{pid: 1})
	srv, paths := newTestServer(t, launcher)
	srv.startedAt = time.Now().UTC()

	if err := srv.writeStatusFile(); err != nil {
		t.Fatalf("writeStatusFile: %v", err)
	}

	info, err := os.Stat(paths.Status)
	if err != nil {
		t.Fatalf("Stat status file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("status file permissions = %04o, want 0600", perm)
	}
}
