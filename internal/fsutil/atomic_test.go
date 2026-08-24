package fsutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWriteFileAtomicBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello atomic world")

	if err := WriteFileAtomic(path, content, 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("content = %q, want %q", data, content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("permissions = %04o, want 0600", got)
		}
	}

	// Overwrite existing file
	newContent := []byte("updated content")
	if err := WriteFileAtomic(path, newContent, 0o600); err != nil {
		t.Fatalf("WriteFileAtomic overwrite: %v", err)
	}

	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after overwrite: %v", err)
	}
	if !bytes.Equal(data, newContent) {
		t.Fatalf("content after overwrite = %q, want %q", data, newContent)
	}

	// Verify no temporary files remain
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "test.txt" {
		t.Fatalf("unexpected directory entries: %+v", entries)
	}
}

func TestWriteFileAtomicConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	type record struct {
		Seq     int    `json:"seq"`
		Padding string `json:"padding"`
	}

	initial := record{Seq: 0, Padding: "initial"}
	initData, _ := json.Marshal(initial)
	if err := WriteFileAtomic(path, initData, 0o600); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	var stop atomic.Bool
	var readerErrors atomic.Int64
	var readCount atomic.Int64
	var wg sync.WaitGroup

	// Launch concurrent reader goroutines
	numReaders := 8
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				data, err := os.ReadFile(path)
				if err != nil {
					// On Windows, ReplaceFileW may briefly leave dst absent
					continue
				}
				if len(data) == 0 {
					readerErrors.Add(1)
					t.Errorf("observed empty file during concurrent read")
					return
				}
				var rec record
				if err := json.Unmarshal(data, &rec); err != nil {
					readerErrors.Add(1)
					t.Errorf("observed corrupted/partial JSON: %v (raw: %q)", err, string(data))
					return
				}
				if rec.Seq < 0 {
					readerErrors.Add(1)
					t.Errorf("invalid sequence number: %d", rec.Seq)
					return
				}
				readCount.Add(1)
			}
		}()
	}

	// Writer publishes new versions sequentially
	numWrites := 150
	for i := 1; i <= numWrites; i++ {
		rec := record{
			Seq:     i,
			Padding: fmt.Sprintf("payload iteration %d with extended text to ensure multi-byte write", i),
		}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if err := WriteFileAtomic(path, data, 0o600); err != nil {
			t.Fatalf("WriteFileAtomic iter %d: %v", i, err)
		}
	}

	stop.Store(true)
	wg.Wait()

	if readerErrors.Load() > 0 {
		t.Fatalf("%d reader errors observed during concurrent writes", readerErrors.Load())
	}
	if readCount.Load() == 0 {
		t.Fatal("no successful reads completed")
	}
}

func TestWriteFileAtomicFaultPreservesExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory permissions differ on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	initialContent := []byte("original protected content")

	if err := WriteFileAtomic(path, initialContent, 0o600); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	// Make parent directory read-only to force failure during temp file creation
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod dir: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()

	// Attempt overwrite which must fail
	err := WriteFileAtomic(path, []byte("new doomed content"), 0o600)
	if err == nil {
		t.Fatal("expected WriteFileAtomic to fail on read-only directory")
	}

	// Restore permissions to inspect destination
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restore Chmod dir: %v", err)
	}

	// Verify original file is intact and unchanged
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, initialContent) {
		t.Fatalf("original content altered: got %q, want %q", data, initialContent)
	}
}

func TestSyncDir(t *testing.T) {
	dir := t.TempDir()
	if err := SyncDir(dir); err != nil {
		t.Fatalf("SyncDir on valid directory failed: %v", err)
	}
}
