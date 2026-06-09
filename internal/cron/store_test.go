package cron

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(StoreOptions{RootDir: t.TempDir(), Now: func() time.Time { return time.Unix(1000, 0).UTC() }})
}

func TestStoreAddListGetRemove(t *testing.T) {
	s := newTestStore(t)
	job := Job{Expr: "0 9 * * *", Prompt: "hi", Status: StatusActive, NextRunAt: time.Unix(2000, 0).UTC()}
	added, err := s.Add(job)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == "" || added.CreatedAt.IsZero() {
		t.Fatalf("Add must assign ID + CreatedAt, got %+v", added)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].ID != added.ID {
		t.Fatalf("List=%v err=%v", list, err)
	}
	got, err := s.Get(added.ID)
	if err != nil || got.Prompt != "hi" || got.Expr != "0 9 * * *" {
		t.Fatalf("Get=%+v err=%v", got, err)
	}
	got.Status = StatusPaused
	if err := s.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if reread, _ := s.Get(added.ID); reread.Status != StatusPaused {
		t.Fatalf("Update not persisted, status=%q", reread.Status)
	}
	if err := s.Remove(added.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("expected empty after remove, got %v", list)
	}
	if _, err := s.Get(added.ID); err == nil {
		t.Fatal("Get of removed id must error")
	}
}

func TestStoreAppendRun(t *testing.T) {
	s := newTestStore(t)
	job, _ := s.Add(Job{Expr: "* * * * *", Prompt: "x", Status: StatusActive})
	for i := 0; i < 3; i++ {
		if err := s.AppendRun(job.ID, RunRecord{JobID: job.ID, At: time.Unix(int64(i), 0).UTC(), ExitCode: i}); err != nil {
			t.Fatalf("AppendRun: %v", err)
		}
	}
	runs, err := s.Runs(job.ID)
	if err != nil || len(runs) != 3 || runs[2].ExitCode != 2 {
		t.Fatalf("Runs=%v err=%v", runs, err)
	}
}

func TestDefaultRootHonorsXDG(t *testing.T) {
	root := DefaultRoot(map[string]string{"XDG_DATA_HOME": "/tmp/xdg"})
	if root != filepath.Join("/tmp/xdg", "zero", "cron") {
		t.Fatalf("DefaultRoot=%q", root)
	}
}
