package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/fsutil"
)

func TestWriteStatusFilePreservesPreviousDocumentWhenReplaceFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.status")
	previous := []byte(`{"pid":7,"socket":"old.sock","version":1,"startedAt":"2026-08-01T00:00:00Z"}`)
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatal(err)
	}

	replaceErr := errors.New("injected replacement failure")
	server := &Server{
		startedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		opts: ServerOptions{
			Paths:   Paths{Status: path},
			Version: 2,
			replaceStatusFile: func(src, dst string) error {
				staged, err := os.ReadFile(src)
				if err != nil {
					t.Fatalf("read staged status: %v", err)
				}
				var decoded StatusFile
				if err := json.Unmarshal(staged, &decoded); err != nil {
					t.Fatalf("staged status is incomplete JSON: %v", err)
				}
				current, err := os.ReadFile(dst)
				if err != nil {
					t.Fatalf("read previous status at commit boundary: %v", err)
				}
				if string(current) != string(previous) {
					t.Fatalf("previous status changed before commit: %q", current)
				}
				return replaceErr
			},
		},
	}

	err := server.writeStatusFile()
	if !errors.Is(err, replaceErr) {
		t.Fatalf("writeStatusFile error = %v, want injected replacement failure", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved status: %v", err)
	}
	if string(current) != string(previous) {
		t.Fatalf("status after failed publication = %q, want previous document", current)
	}
	assertNoStatusTemps(t, dir)
}

func TestWriteStatusFilePublishesCompleteRestrictedDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.status")
	if err := os.WriteFile(path, []byte(`{"pid":7}`), 0o644); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	parentSynced := false
	server := &Server{
		startedAt: startedAt,
		opts: ServerOptions{
			Paths:   Paths{Socket: filepath.Join(dir, "daemon.sock"), Status: path},
			Version: 3,
			syncStatusParent: func(got string) error {
				if got != dir {
					t.Fatalf("synced parent = %q, want %q", got, dir)
				}
				parentSynced = true
				return nil
			},
		},
	}

	if err := server.writeStatusFile(); err != nil {
		t.Fatalf("writeStatusFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var status StatusFile
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("published status is invalid JSON: %v", err)
	}
	if status.PID != os.Getpid() || status.Socket != server.opts.Paths.Socket || status.Version != 3 || !status.StartedAt.Equal(startedAt) {
		t.Fatalf("published status = %+v", status)
	}
	if !parentSynced {
		t.Fatal("status parent directory was not synced")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("status mode = %04o, want 0600", got)
		}
	}
	assertNoStatusTemps(t, dir)
}

func TestWriteStatusFileReaderSeesCompleteDocumentsDuringPublication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.status")
	previous := StatusFile{
		PID:       7,
		Socket:    filepath.Join(dir, "old.sock"),
		Version:   1,
		StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	previousData, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, previousData, 0o600); err != nil {
		t.Fatal(err)
	}

	replacementReady := make(chan struct{})
	publish := make(chan struct{})
	writeDone := make(chan error, 1)
	server := &Server{
		startedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		opts: ServerOptions{
			Paths:   Paths{Socket: filepath.Join(dir, "new.sock"), Status: path},
			Version: 2,
			replaceStatusFile: func(src, dst string) error {
				close(replacementReady)
				<-publish
				return fsutil.ReplaceWithRetry(src, dst, nil)
			},
		},
	}
	go func() {
		writeDone <- server.writeStatusFile()
	}()

	<-replacementReady
	for range 100 {
		status := readStatusDocument(t, path)
		if status != previous {
			t.Fatalf("status before commit = %+v, want previous document %+v", status, previous)
		}
	}
	close(publish)
	if err := <-writeDone; err != nil {
		t.Fatalf("writeStatusFile: %v", err)
	}

	status := readStatusDocument(t, path)
	if status.PID != os.Getpid() || status.Socket != server.opts.Paths.Socket || status.Version != 2 || !status.StartedAt.Equal(server.startedAt) {
		t.Fatalf("status after commit = %+v", status)
	}
	assertNoStatusTemps(t, dir)
}

func TestWriteStatusFileContinuesAfterCommittedReplacementWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.status")
	if err := os.WriteFile(path, []byte(`{"pid":7}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupErr := errors.New("injected backup cleanup failure")
	var logs []string
	parentSynced := false
	server := &Server{
		startedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		opts: ServerOptions{
			Paths:   Paths{Socket: filepath.Join(dir, "daemon.sock"), Status: path},
			Version: 4,
			Log:     func(message string) { logs = append(logs, message) },
			replaceStatusFile: func(src, dst string) error {
				if err := fsutil.ReplaceWithRetry(src, dst, nil); err != nil {
					return err
				}
				return &fsutil.CommittedReplacementCleanupError{
					BackupPath: filepath.Join(dir, ".zero-replace-backup"),
					Cause:      cleanupErr,
				}
			},
			syncStatusParent: func(string) error {
				parentSynced = true
				return nil
			},
		},
	}

	if err := server.writeStatusFile(); err != nil {
		t.Fatalf("writeStatusFile returned a post-commit warning as failure: %v", err)
	}
	if !parentSynced {
		t.Fatal("status parent directory was not synced after committed replacement warning")
	}
	status := readStatusDocument(t, path)
	if status.Version != 4 {
		t.Fatalf("published status = %+v, want version 4", status)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], cleanupErr.Error()) {
		t.Fatalf("logs = %q, want committed cleanup warning", logs)
	}
	assertNoStatusTemps(t, dir)
}

func TestWriteStatusFileContinuesAfterDirectorySyncWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.status")
	if err := os.WriteFile(path, []byte(`{"pid":7}`), 0o600); err != nil {
		t.Fatal(err)
	}

	syncErr := errors.New("injected directory sync failure")
	var logs []string
	server := &Server{
		startedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		opts: ServerOptions{
			Paths:            Paths{Socket: filepath.Join(dir, "daemon.sock"), Status: path},
			Version:          5,
			Log:              func(message string) { logs = append(logs, message) },
			syncStatusParent: func(string) error { return syncErr },
		},
	}

	if err := server.writeStatusFile(); err != nil {
		t.Fatalf("writeStatusFile returned a post-commit warning as failure: %v", err)
	}
	status := readStatusDocument(t, path)
	if status.Version != 5 {
		t.Fatalf("published status = %+v, want version 5", status)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], syncErr.Error()) {
		t.Fatalf("logs = %q, want directory sync warning", logs)
	}
	assertNoStatusTemps(t, dir)
}

func readStatusDocument(t *testing.T, path string) StatusFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var status StatusFile
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("status is incomplete JSON: %v (content %q)", err, data)
	}
	return status
}

func assertNoStatusTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, statusTempPattern))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary status files remain: %v", matches)
	}
}
