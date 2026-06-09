package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/cron"
)

// execRunner runs a `zero exec ...` invocation and returns its exit code. The
// default is cli.Run; tests inject a fake.
type execRunner func(args []string, stdout, stderr io.Writer) int

// cronRun implements `zero cron run [--once] [--catch-up] [id...]`.
func cronRun(store *cron.Store, now func() time.Time, args []string, stdout io.Writer, stderr io.Writer, exec execRunner) int {
	once, catchUp := false, false
	var ids []string
	for _, a := range args {
		switch {
		case a == "--once":
			once = true
		case a == "--catch-up":
			catchUp = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "Unknown cron run flag: %s\n", a)
			return exitUsage
		default:
			ids = append(ids, a)
		}
	}

	selected := func(j cron.Job) bool {
		if j.Status != cron.StatusActive {
			return false
		}
		if len(ids) == 0 {
			return true
		}
		return contains(ids, j.ID)
	}

	fireDue := func() {
		jobs, err := store.List()
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return
		}
		for _, j := range jobs {
			if !selected(j) || j.NextRunAt.After(now()) {
				continue
			}
			fireJob(store, now, j, stdout, exec)
		}
	}

	if once {
		fireDue()
		return exitSuccess
	}

	// Startup: reconcile overdue jobs. Without --catch-up, an overdue job is
	// rescheduled to its next future slot (skip) instead of firing the backlog.
	if !catchUp {
		jobs, err := store.List()
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitCrash
		}
		for _, j := range jobs {
			if !selected(j) || j.NextRunAt.After(now()) {
				continue
			}
			if sched, perr := cron.Parse(j.Expr); perr == nil {
				if nxt := sched.Next(now()); !nxt.IsZero() {
					j.NextRunAt = nxt
					_ = store.Update(j)
				}
			}
		}
	}

	ctx, stop := signalContext()
	defer stop()
	fireDue() // fire anything already due before the first tick
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "cron scheduler stopped.")
			return exitSuccess
		case <-ticker.C:
			fireDue()
		}
	}
}

// contains reports whether ss contains want.
func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// fireJob runs one job via the exec runner, records the outcome, advances the
// schedule, and persists. The foreground loop is single-goroutine, so the
// previous fire has already returned before the next tick — no overlap.
func fireJob(store *cron.Store, now func() time.Time, job cron.Job, stdout io.Writer, exec execRunner) {
	fired := now()
	args := []string{"exec", "--output-format", "stream-json", "--session-title", "cron:" + job.ID}
	if job.Cwd != "" {
		args = append(args, "--cwd", job.Cwd)
	}
	if job.Model != "" {
		args = append(args, "--model", job.Model)
	}
	args = append(args, "--prompt", job.Prompt)

	var outBuf, errBuf strings.Builder
	code := exec(args, &outBuf, &errBuf)
	_ = store.AppendRun(job.ID, cron.RunRecord{JobID: job.ID, At: fired, ExitCode: code, SessionTitle: "cron:" + job.ID})

	job.FireCount++
	if sched, err := cron.Parse(job.Expr); err == nil {
		job.NextRunAt = sched.Next(fired)
	}
	_ = store.Update(job)
	fmt.Fprintf(stdout, "fired %s -> exit %d (next: %s)\n", job.ID, code, formatCronTime(job.NextRunAt))
}
