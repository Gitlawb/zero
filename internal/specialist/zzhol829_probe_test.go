package specialist

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// PROBE 1: does a WRITE-CAPABLE plan admit many workers sharing one worktree?
func TestZZProbeConcurrentWritersShareOneTree(t *testing.T) {
	limits := Limits{MaxTasks: 20, ParentTools: []string{"read_file", "edit_file", "bash"}}
	tasks := []any{
		map[string]any{"id": "w1", "prompt": "edit", "tools": []any{"edit_file"}},
		map[string]any{"id": "w2", "prompt": "edit", "tools": []any{"edit_file"}},
		map[string]any{"id": "w3", "prompt": "shell", "tools": []any{"bash"}},
		map[string]any{"id": "w4", "prompt": "shell", "tools": []any{"bash"}},
	}
	plan, err := ParsePlan(planArgs(tasks, map[string]any{"max_workers": float64(4)}), limits)
	if err != nil {
		t.Fatalf("a write-capable plan with 4 workers was REFUSED: %v", err)
	}
	t.Logf("ACCEPTED: max_workers=%d, RequiresIsolation=%v", plan.Budget().MaxWorkers, plan.RequiresIsolation())

	workspace := PlanWorkspace{Path: "C:/tmp/plan-worktree", Isolated: true, Release: func() {}}

	var mu sync.Mutex
	cwds := map[string]string{}
	toolsSeen := map[string][]string{}
	inFlight, peak := 0, 0
	report := ExecutePlanIn(context.Background(), plan, workspace, limits.ParentTools,
		func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			cwds[req.Task.ID] = req.Cwd
			toolsSeen[req.Task.ID] = req.Tools
			mu.Unlock()
			time.Sleep(40 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return TaskResult{Outcome: TaskSucceeded}, nil
		}, nil)

	t.Logf("peak concurrent writers = %d (workers=%d)", peak, report.Workers)
	for id, cwd := range cwds {
		t.Logf("task %s cwd=%q tools=%v", id, cwd, toolsSeen[id])
	}
}

// PROBE 2: head-of-line blocking. Two independent chains; the walk is parked on
// a task whose dependency is slow, so a runnable task from the other chain waits
// behind it even though a worker is free.
func TestZZProbeHeadOfLineBlocking(t *testing.T) {
	if got := effectivePlanWorkers(2); got < 2 {
		t.Skipf("host allows only %d workers", got)
	}
	const slow = 400 * time.Millisecond
	const quick = 20 * time.Millisecond

	// Declaration order matters for the Kahn seed: slowHead, fastHead are the
	// zero-indegree nodes.
	plan := mustPlan(t, []any{
		task("slowHead", "slow"),
		task("fastHead", "quick"),
		task("slowTail", "quick", "slowHead"),
		task("fastTail", "slow", "fastHead"),
	}, map[string]any{"max_workers": float64(2)}, readOnlyLimits())
	t.Logf("order = %v", plan.Order())

	durations := map[string]time.Duration{
		"slowHead": slow, "fastHead": quick, "slowTail": quick, "fastTail": slow,
	}

	var mu sync.Mutex
	start := time.Now()
	startedAt := map[string]time.Duration{}
	endedAt := map[string]time.Duration{}

	wall := time.Now()
	report := ExecutePlan(context.Background(), plan, []string{"read_file"},
		func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
			mu.Lock()
			startedAt[req.Task.ID] = time.Since(start)
			mu.Unlock()
			time.Sleep(durations[req.Task.ID])
			mu.Lock()
			endedAt[req.Task.ID] = time.Since(start)
			mu.Unlock()
			return TaskResult{Outcome: TaskSucceeded}, nil
		}, nil)
	elapsed := time.Since(wall)

	for _, id := range plan.Order() {
		t.Logf("%-9s started %6s  ended %6s", id, startedAt[id].Round(time.Millisecond), endedAt[id].Round(time.Millisecond))
	}
	t.Logf("WALL CLOCK = %s", elapsed.Round(time.Millisecond))
	t.Logf("report: sequential=%s criticalPath=%s max_speedup=%.2f workers=%d",
		report.SequentialTotal.Round(time.Millisecond), report.CriticalPath.Round(time.Millisecond),
		report.MaxSpeedup, report.Workers)
	t.Logf("ACTUAL speedup = %.2f", float64(report.SequentialTotal)/float64(elapsed))
	t.Logf("fastTail was runnable at %s (fastHead ended) but started at %s",
		endedAt["fastHead"].Round(time.Millisecond), startedAt["fastTail"].Round(time.Millisecond))
}

// PROBE 3: a task that failed for a REAL reason, harvested after the plan's
// context is cancelled, is relabelled as cancelled.
type zzPauseRecorder struct {
	gate  chan struct{}
	once  sync.Once
	calls atomic.Int32
}

func (r *zzPauseRecorder) TaskDispatched(Task)            {}
func (r *zzPauseRecorder) TaskCompleted(TaskResult)       {}
func (r *zzPauseRecorder) TaskFailed(TaskResult)          {}
func (r *zzPauseRecorder) PlanRunning(context.CancelFunc) {}
func (r *zzPauseRecorder) WaitWhilePaused(ctx context.Context) {
	// Park the scheduler at the SECOND task boundary, i.e. after "boom" has been
	// dispatched. The first boundary must pass through or nothing ever runs.
	if r.calls.Add(1) < 2 {
		return
	}
	r.once.Do(func() {
		select {
		case <-r.gate:
		case <-ctx.Done():
		}
	})
}

func TestZZProbeRealFailureRelabelledCancelled(t *testing.T) {
	plan := mustPlan(t, []any{
		task("boom", "fails for real"),
		task("later", "never runs"),
	}, map[string]any{"max_workers": float64(2)}, readOnlyLimits())
	t.Logf("order = %v", plan.Order())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &zzPauseRecorder{gate: make(chan struct{})}

	failed := make(chan struct{})
	done := make(chan PlanReport, 1)
	go func() {
		done <- ExecutePlan(ctx, plan, []string{"read_file"},
			func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
				if req.Task.ID == "boom" {
					close(failed)
					// A REAL failure: not a context error.
					return TaskResult{Outcome: TaskFailed, Err: "compile error in main.go"}, nil
				}
				return TaskResult{Outcome: TaskSucceeded}, nil
			}, rec)
	}()

	<-failed
	time.Sleep(50 * time.Millisecond) // let the completion sit in the buffer
	cancel()                          // user stops the paused plan
	close(rec.gate)

	report := <-done
	for _, task := range report.Tasks {
		t.Logf("task %-6s outcome=%-18s err=%q", task.ID, task.Outcome, task.Err)
	}
	t.Logf("status=%s succeeded=%d failed=%d cancelled=%d skipped=%d",
		report.Status, report.Succeeded, report.Failed, report.Cancelled, report.Skipped)
}
