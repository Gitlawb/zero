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
	if args[0] != "exec" || !contains(args, "--prompt=fire me") {
		t.Fatalf("fire must shell exec with inline --prompt=: %v", args)
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

func TestCronRunPausesUnadvanceableJob(t *testing.T) {
	now := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)
	store := cron.NewStore(cron.StoreOptions{RootDir: t.TempDir(), Now: func() time.Time { return now }})
	// An impossible schedule stored directly (cronAdd would reject it). After it
	// fires once it cannot advance, so it must be paused, not re-fired forever.
	job, _ := store.Add(cron.Job{Expr: "0 0 30 2 *", Prompt: "x", Status: cron.StatusActive, NextRunAt: now.Add(-time.Minute)})
	fx := &fakeExec{}
	var out, errb bytes.Buffer
	cronRun(store, func() time.Time { return now }, []string{"--once"}, &out, &errb, fx.run)
	d, _ := store.Get(job.ID)
	if d.Status != cron.StatusPaused {
		t.Fatalf("unadvanceable job must be paused, got status=%q", d.Status)
	}
	runs, _ := store.Runs(job.ID)
	if len(runs) != 1 || runs[0].Error == "" {
		t.Fatalf("expected one run record with an error, got %+v", runs)
	}
}

func TestCronRunDashLeadingPromptUsesInlineForm(t *testing.T) {
	now := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)
	store := cron.NewStore(cron.StoreOptions{RootDir: t.TempDir(), Now: func() time.Time { return now }})
	if _, err := store.Add(cron.Job{Expr: "* * * * *", Prompt: "- do a thing", Status: cron.StatusActive, NextRunAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	fx := &fakeExec{}
	var out, errb bytes.Buffer
	cronRun(store, func() time.Time { return now }, []string{"--once"}, &out, &errb, fx.run)
	if len(fx.calls) != 1 || !contains(fx.calls[0], "--prompt=- do a thing") {
		t.Fatalf("dash-leading prompt must use inline --prompt= form: %v", fx.calls)
	}
}

func TestReconcileOverdueKeepsExactlyDueJob(t *testing.T) {
	now := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)
	store := cron.NewStore(cron.StoreOptions{RootDir: t.TempDir(), Now: func() time.Time { return now }})
	exact, _ := store.Add(cron.Job{Expr: "0 9 * * *", Prompt: "exact", Status: cron.StatusActive, NextRunAt: now})
	overdue, _ := store.Add(cron.Job{Expr: "0 9 * * *", Prompt: "old", Status: cron.StatusActive, NextRunAt: now.Add(-24 * time.Hour)})
	var errb bytes.Buffer
	reconcileOverdue(store, func() time.Time { return now }, nil, &errb)
	if e, _ := store.Get(exact.ID); !e.NextRunAt.Equal(now) {
		t.Fatalf("exactly-due job must not be rescheduled, got %v", e.NextRunAt)
	}
	if o, _ := store.Get(overdue.ID); !o.NextRunAt.After(now) {
		t.Fatalf("strictly-overdue job must be pushed to the future, got %v", o.NextRunAt)
	}
}
