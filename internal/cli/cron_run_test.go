package cli

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/cron"
)

// fakeExec records each invocation and returns a fixed exit code.
type fakeExec struct {
	mu    sync.Mutex
	calls [][]string
	code  int
}

func (f *fakeExec) run(args []string, stdout, stderr io.Writer) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	return f.code
}

func TestCronRunOnceFiresDueJobs(t *testing.T) {
	now := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)
	store := cron.NewStore(cron.StoreOptions{RootDir: t.TempDir(), Now: func() time.Time { return now }})
	due, _ := store.Add(cron.Job{Expr: "0 9 * * *", Prompt: "fire me", Status: cron.StatusActive, NextRunAt: now.Add(-time.Minute)})
	notDue, _ := store.Add(cron.Job{Expr: "0 9 * * *", Prompt: "later", Status: cron.StatusActive, NextRunAt: now.Add(time.Hour)})
	paused, _ := store.Add(cron.Job{Expr: "0 9 * * *", Prompt: "paused", Status: cron.StatusPaused, NextRunAt: now.Add(-time.Hour)})

	fx := &fakeExec{}
	var out, errb bytes.Buffer
	code := cronRun(store, func() time.Time { return now }, []string{"--once"}, &out, &errb, fx.run)
	if code != 0 {
		t.Fatalf("run --once exit=%d err=%s", code, errb.String())
	}
	if len(fx.calls) != 1 {
		t.Fatalf("expected exactly 1 fire (the due job), got %d: %v", len(fx.calls), fx.calls)
	}
	args := fx.calls[0]
	if args[0] != "exec" || !contains(args, "--prompt") || !contains(args, "fire me") {
		t.Fatalf("fire must shell exec with --prompt: %v", args)
	}
	if !contains(args, "--output-format") || !contains(args, "stream-json") {
		t.Fatalf("fire must use stream-json for session persistence: %v", args)
	}
	// due job rescheduled forward + fireCount incremented + run recorded
	d, _ := store.Get(due.ID)
	if d.FireCount != 1 || !d.NextRunAt.After(now) {
		t.Fatalf("due job not advanced: %+v", d)
	}
	runs, _ := store.Runs(due.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run record, got %d", len(runs))
	}
	if r, _ := store.Get(notDue.ID); r.FireCount != 0 {
		t.Fatal("not-due job must not fire")
	}
	if r, _ := store.Get(paused.ID); r.FireCount != 0 {
		t.Fatal("paused job must not fire")
	}
}

func TestCronRunOnceSkipsOverdueWithoutCatchUp(t *testing.T) {
	now := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)
	store := cron.NewStore(cron.StoreOptions{RootDir: t.TempDir(), Now: func() time.Time { return now }})
	// NextRunAt far in the past, but we still fire once because it's due; the
	// distinction this test pins: after firing, it reschedules to a FUTURE slot.
	job, _ := store.Add(cron.Job{Expr: "0 9 * * *", Prompt: "x", Status: cron.StatusActive, NextRunAt: now.Add(-72 * time.Hour)})
	fx := &fakeExec{}
	var out, errb bytes.Buffer
	cronRun(store, func() time.Time { return now }, []string{"--once"}, &out, &errb, fx.run)
	d, _ := store.Get(job.ID)
	if !d.NextRunAt.After(now) {
		t.Fatalf("after firing, next run must be in the future, got %v", d.NextRunAt)
	}
}
