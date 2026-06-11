package cron

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreGetDistinguishesNotFoundFromReadError(t *testing.T) {
	s := newTestStore(t)

	// A genuinely absent job → ErrJobNotFound.
	if _, err := s.Get("job_missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing job Get err = %v, want ErrJobNotFound", err)
	}

	// A job whose metadata.json can't be read (here it's a directory, so ReadFile
	// fails with a non-ENOENT error) must NOT be reported as not-found — otherwise
	// cron_run mislabels a transient IO failure as "job removed during run".
	id := "job_unreadable"
	if err := os.MkdirAll(filepath.Join(s.jobDir(id), "metadata.json"), 0o755); err != nil {
		t.Fatalf("mkdir metadata.json-as-dir: %v", err)
	}
	_, err := s.Get(id)
	if err == nil {
		t.Fatal("expected an error reading a directory as metadata.json")
	}
	if errors.Is(err, ErrJobNotFound) {
		t.Fatalf("a read error must not be reported as ErrJobNotFound: %v", err)
	}
}

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

func TestDefaultRootEmptyHomeFallsBackToUserHome(t *testing.T) {
	// No XDG_DATA_HOME and no HOME: must NOT produce a relative ".local/share"
	// under the caller's cwd (the bug). It falls back to the OS user home.
	root := DefaultRoot(map[string]string{})
	if !filepath.IsAbs(root) {
		t.Fatalf("DefaultRoot with empty env must be absolute, got %q", root)
	}
	if strings.HasPrefix(root, ".local") || strings.HasPrefix(root, filepath.Join(".local", "share")) {
		t.Fatalf("DefaultRoot leaked a relative .local/share path: %q", root)
	}
	if filepath.Base(root) != "cron" || filepath.Base(filepath.Dir(root)) != "zero" {
		t.Fatalf("DefaultRoot tail = %q, want .../zero/cron", root)
	}
}

func TestStoreRejectsUnsafeID(t *testing.T) {
	root := t.TempDir()
	s := NewStore(StoreOptions{RootDir: root, Now: func() time.Time { return time.Unix(0, 0).UTC() }})
	sibling := filepath.Join(filepath.Dir(root), "victim")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../victim", "a/b", "..", ""} {
		if err := s.Remove(id); err == nil {
			t.Fatalf("Remove(%q) must be rejected", id)
		}
		if _, err := s.Get(id); err == nil {
			t.Fatalf("Get(%q) must be rejected", id)
		}
		if _, err := s.Runs(id); err == nil {
			t.Fatalf("Runs(%q) must be rejected", id)
		}
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("traversal must not delete a sibling directory: %v", err)
	}
}

func TestListSurfacesCorruptJob(t *testing.T) {
	s := newTestStore(t)
	good, _ := s.Add(Job{Expr: "0 9 * * *", Prompt: "ok", Status: StatusActive})
	bad, _ := s.Add(Job{Expr: "0 9 * * *", Prompt: "bad", Status: StatusActive})
	if err := os.WriteFile(filepath.Join(s.jobDir(bad.ID), "metadata.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.List()
	if err == nil {
		t.Fatal("List should surface a corrupt job via a non-nil (warning) error")
	}
	if len(jobs) != 1 || jobs[0].ID != good.ID {
		t.Fatalf("good job must still list despite a corrupt sibling, got %+v", jobs)
	}
}
