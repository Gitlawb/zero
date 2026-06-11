package cli

import (
	"errors"
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
			fmt.Fprintln(stderr, "warning:", err.Error()) // jobs still valid; never fatal
		}
		for _, j := range jobs {
			if !selected(j) || j.NextRunAt.After(now()) {
				continue
			}
			fireJob(store, now, j, stdout, stderr, exec)
		}
	}

	if once {
		// --once fires every currently-due job once and exits, so --catch-up is a
		// no-op when combined with --once. Intended for use under an external
		// scheduler (system cron / launchd).
		fireDue()
		return exitSuccess
	}

	// Forever-mode startup: unless --catch-up, push STRICTLY-overdue jobs to their
	// next future slot (skip the backlog) without firing.
	if !catchUp {
		reconcileOverdue(store, now, ids, stderr)
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

// reconcileOverdue (forever-mode startup, non --catch-up) reschedules jobs that
// are STRICTLY overdue (NextRunAt before the current minute) to their next future
// slot without firing the backlog. A job due within the current minute is left
// for the fireDue pass so it still fires now. List errors are warnings, never
// fatal (jobs remains valid).
func reconcileOverdue(store *cron.Store, now func() time.Time, ids []string, stderr io.Writer) {
	jobs, err := store.List()
	if err != nil {
		fmt.Fprintln(stderr, "warning:", err.Error())
	}
	nowMin := now().Truncate(time.Minute)
	for _, j := range jobs {
		if j.Status != cron.StatusActive {
			continue
		}
		if len(ids) > 0 && !contains(ids, j.ID) {
			continue
		}
		if !j.NextRunAt.Before(nowMin) {
			continue // not strictly overdue
		}
		if sched, perr := cron.Parse(j.Expr); perr == nil {
			if nxt := sched.Next(now()); !nxt.IsZero() {
				j.NextRunAt = nxt
				if err := store.Update(j); err != nil {
					fmt.Fprintf(stderr, "warning: failed to reschedule %s: %v\n", j.ID, err)
				}
			}
		}
	}
}

// fireJob runs one job via the exec runner, records the outcome, advances the
// schedule, and persists. The foreground loop is single-goroutine, so the
// previous fire has already returned before the next tick — no overlap.
func fireJob(store *cron.Store, now func() time.Time, job cron.Job, stdout io.Writer, stderr io.Writer, exec execRunner) {
	fired := now()
	args := []string{"exec", "--output-format", "stream-json", "--session-title", "cron:" + job.ID}
	if job.Cwd != "" {
		args = append(args, "--cwd", job.Cwd)
	}
	if job.Model != "" {
		args = append(args, "--model", job.Model)
	}
	// Inline --prompt= form: a bare "--prompt" "<value>" makes exec reject a
	// dash-leading prompt as a misplaced flag; the =VALUE form is taken verbatim.
	args = append(args, "--prompt="+job.Prompt)

	var outBuf, errBuf strings.Builder
	code := exec(args, &outBuf, &errBuf)

	rec := cron.RunRecord{JobID: job.ID, At: fired, ExitCode: code, SessionTitle: "cron:" + job.ID}
	if code != 0 {
		rec.Error = cronTruncate(strings.TrimSpace(errBuf.String()), 500)
	}

	job.FireCount++
	// Advance the schedule. If the expression can no longer produce a future run
	// (became invalid, or is an impossible spec whose Next is zero), pause the job
	// so it cannot re-fire on every tick.
	if sched, perr := cron.Parse(job.Expr); perr != nil {
		job.Status = cron.StatusPaused
		if rec.Error == "" {
			rec.Error = "invalid schedule; job paused: " + perr.Error()
		}
	} else if nxt := sched.Next(fired); nxt.IsZero() {
		job.Status = cron.StatusPaused
		if rec.Error == "" {
			rec.Error = "schedule no longer fires; job paused"
		}
	} else {
		job.NextRunAt = nxt
	}
	// Re-read before persisting: the job may have been paused or removed while it
	// was executing, and this in-memory copy is stale from tick start. Without
	// this, store.Update would clobber an external pause back to active. (A single
	// scheduler is the supported model; this narrows but does not fully close the
	// read-modify-write window — full atomicity needs file locking.)
	current, err := store.Get(job.ID)
	switch {
	case errors.Is(err, cron.ErrJobNotFound):
		// Genuinely removed mid-run: don't recreate it by persisting.
		fmt.Fprintf(stdout, "fired %s -> exit %d (job removed during run)\n", job.ID, code)
		return
	case err != nil:
		// A transient read failure (IO/permission) is NOT removal — warn but still
		// record the run and persist the computed next state below.
		fmt.Fprintf(stderr, "warning: could not re-read job %s before persist: %v\n", job.ID, err)
	case current.Status == cron.StatusPaused:
		job.Status = cron.StatusPaused
	}
	if err := store.AppendRun(job.ID, rec); err != nil {
		fmt.Fprintf(stderr, "warning: failed to record run for %s: %v\n", job.ID, err)
	}
	if err := store.Update(job); err != nil {
		fmt.Fprintf(stderr, "warning: failed to persist job state for %s: %v\n", job.ID, err)
	}
	fmt.Fprintf(stdout, "fired %s -> exit %d (next: %s)\n", job.ID, code, formatCronTime(job.NextRunAt))
}

func cronTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
