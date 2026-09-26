package cron

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAppendRunRetainsNewestThousand(t *testing.T) {
	store := newTestStore(t)
	job, err := store.Add(Job{Expr: "* * * * *", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed an oversized legacy log, then cross the boundary again after compaction.
	var history bytes.Buffer
	for i := 0; i < 1207; i++ {
		if err := json.NewEncoder(&history).Encode(RunRecord{ExitCode: i}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(store.jobDir(job.ID), "runs.jsonl")
	if err := os.WriteFile(path, history.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, next := range []int{1207, 1208} {
		if err := store.AppendRun(job.ID, RunRecord{ExitCode: next}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		if len(lines) != 1000 {
			t.Fatalf("retained %d records, want 1000", len(lines))
		}
		for i, line := range lines {
			var rec RunRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				t.Fatal(err)
			}
			if rec.ExitCode != next-999+i {
				t.Fatalf("record %d = %d, want %d", i, rec.ExitCode, next-999+i)
			}
		}
	}
}

// AppendRun on a job removed mid-run must NOT resurrect its directory (which the
// old unconditional MkdirAll did, leaving an orphaned runs.jsonl with no
// metadata.json).
func TestAppendRunDoesNotResurrectRemovedJob(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	job, err := store.Add(Job{Expr: "* * * * *", Prompt: "x"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Remove(job.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if err := store.AppendRun(job.ID, RunRecord{JobID: job.ID, At: store.now()}); err != nil {
		t.Fatalf("AppendRun after Remove should be a no-op, got: %v", err)
	}
	if _, err := os.Stat(store.jobDir(job.ID)); !os.IsNotExist(err) {
		t.Fatalf("AppendRun resurrected the removed job directory (stat err = %v)", err)
	}
	runs, err := store.Runs(job.ID, 0)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs for a removed job, got %d", len(runs))
	}
}

func TestRunsTail(t *testing.T) {
	store := newTestStore(t)
	job, err := store.Add(Job{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var history bytes.Buffer
	// A forward scan would fail on this old oversized line. A recent-window
	// reader must stop before reaching it, rather than merely cap its output.
	history.WriteString(strings.Repeat("x", 1024*1024+1) + "\n")
	for i := 0; i < 1103; i++ {
		if err := json.NewEncoder(&history).Encode(RunRecord{ExitCode: i, SessionTitle: strings.Repeat("界", 1500)}); err != nil {
			t.Fatal(err)
		}
	}
	history.WriteString("bad json\n\n{\"exitCode\":1103}") // no final newline
	path := filepath.Join(store.jobDir(job.ID), "runs.jsonl")
	if err := os.WriteFile(path, history.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 7, 999, 1000, 1001, 0, -1} {
		runs, err := store.Runs(job.ID, limit)
		want := limit
		if want <= 0 || want > 1000 {
			want = 1000
		}
		if err != nil || len(runs) != want {
			t.Fatalf("limit %d: got %d records, err %v", limit, len(runs), err)
		}
		for i, run := range runs {
			if run.ExitCode != 1104-want+i {
				t.Fatalf("limit %d, record %d = %d", limit, i, run.ExitCode)
			}
			if run.ExitCode != 1103 && run.SessionTitle != strings.Repeat("界", 1500) {
				t.Fatal("record spanning read blocks was corrupted")
			}
		}
	}
}

func TestAppendRunFailurePreservesHistory(t *testing.T) {
	store := newTestStore(t)
	job, err := store.Add(Job{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.jobDir(job.ID), "runs.jsonl")
	for _, data := range []string{"{\"exitCode\":7}\n", strings.Repeat("x", 1024*1024)} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		rec := RunRecord{ExitCode: 8}
		if strings.HasPrefix(data, "{") {
			rec.Error = strings.Repeat("x", 1024*1024)
		}
		if err := store.AppendRun(job.ID, rec); err == nil {
			t.Fatal("expected oversized new or existing record error")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != data {
			t.Fatalf("failed append changed history: %v", err)
		}
	}
}

func TestRunHistoryConcurrentStores(t *testing.T) {
	store := newTestStore(t)
	job, err := store.Add(Job{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var history bytes.Buffer
	for i := 0; i < 1000; i++ {
		if err := json.NewEncoder(&history).Encode(RunRecord{ExitCode: -1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(store.jobDir(job.ID), "runs.jsonl"), history.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			other := NewStore(StoreOptions{RootDir: store.root})
			if err := other.AppendRun(job.ID, RunRecord{ExitCode: i}); err != nil {
				t.Error(err)
				return
			}
			runs, err := other.Runs(job.ID, 1000)
			if err != nil || len(runs) != 1000 {
				t.Errorf("inconsistent history: %d records, err %v", len(runs), err)
			}
		})
	}
	wg.Wait()
	runs, err := store.Runs(job.ID, 12)
	if err != nil || len(runs) != 12 {
		t.Fatalf("final tail: %d records, err %v", len(runs), err)
	}
	seen := make(map[int]bool)
	for _, run := range runs {
		if run.ExitCode < 0 || run.ExitCode >= 12 || seen[run.ExitCode] {
			t.Fatalf("lost or duplicate concurrent append: %+v", runs)
		}
		seen[run.ExitCode] = true
	}
}
