package cron

import (
	"os"
	"testing"
)

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
	runs, err := store.Runs(job.ID)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs for a removed job, got %d", len(runs))
	}
}
