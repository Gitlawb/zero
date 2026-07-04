package cli

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/cron"
)

func TestFireJobClaimPreventsDoubleFire(t *testing.T) {
	now := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return now }
	store := cron.NewStore(cron.StoreOptions{RootDir: t.TempDir(), Now: nowFn})
	due, _ := store.Add(cron.Job{Expr: "0 9 * * *", Prompt: "fire me", Status: cron.StatusActive, NextRunAt: now.Add(-time.Minute)})

	fx := &fakeExec{}
	// Two schedulers fire the same due job concurrently; the atomic claim must let
	// exactly one through, so the job never double-fires (M9).
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			job, _ := store.Get(due.ID)
			var out, errb bytes.Buffer
			fireJob(store, nowFn, job, &out, &errb, fx.run)
		})
	}
	wg.Wait()

	if len(fx.calls) != 1 {
		t.Fatalf("claim must let exactly one scheduler fire the due job, got %d", len(fx.calls))
	}
	// And NextRunAt is advanced to the next slot (tomorrow 09:00), not left due.
	d, _ := store.Get(due.ID)
	if !d.NextRunAt.After(now) {
		t.Fatalf("NextRunAt must advance past now, got %s", d.NextRunAt)
	}
}
