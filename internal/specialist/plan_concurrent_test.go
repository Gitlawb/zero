package specialist

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// concurrencyProbe records the maximum number of tasks in flight at once, and
// the order dispatches began.
type concurrencyProbe struct {
	mu       sync.Mutex
	inFlight int
	peak     int
	started  []string
	release  chan struct{}
}

func (p *concurrencyProbe) runner() PlanRunner {
	return func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
		p.mu.Lock()
		p.inFlight++
		if p.inFlight > p.peak {
			p.peak = p.inFlight
		}
		p.started = append(p.started, req.Task.ID)
		p.mu.Unlock()

		if p.release != nil {
			<-p.release
		}
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
		return TaskResult{Outcome: TaskSucceeded}, nil
	}
}

func fanOutPlan(t *testing.T, workers int, n int) Plan {
	t.Helper()
	tasks := make([]any, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, task(string(rune('a'+i)), "x"))
	}
	budget := map[string]any{"max_workers": float64(workers)}
	return mustPlan(t, tasks, budget, readOnlyLimits())
}

// ONE WORKER IS STILL ONE AT A TIME. The whole equivalence claim rests on this:
// there is one scheduler, and with a single worker it must never overlap.
func TestOneWorkerNeverOverlaps(t *testing.T) {
	probe := &concurrencyProbe{}
	report := ExecutePlan(context.Background(), fanOutPlan(t, 1, 6), []string{"read_file"}, probe.runner(), nil)
	if probe.peak != 1 {
		t.Fatalf("peak concurrency %d with one worker; it must be 1", probe.peak)
	}
	if report.Succeeded != 6 {
		t.Fatalf("report = %+v", report)
	}
	if report.Workers != 1 || report.WorkersRequested != 1 {
		t.Fatalf("workers reported %d/%d", report.Workers, report.WorkersRequested)
	}
}

// ...and one worker dispatches in the plan's validated order, which is what
// makes the sequential path reproducible rather than merely serial.
func TestOneWorkerDispatchesInPlanOrder(t *testing.T) {
	probe := &concurrencyProbe{}
	plan := fanOutPlan(t, 1, 6)
	ExecutePlan(context.Background(), plan, []string{"read_file"}, probe.runner(), nil)
	for index, id := range plan.Order() {
		if probe.started[index] != id {
			t.Fatalf("dispatch order %v, want the plan's order %v", probe.started, plan.Order())
		}
	}
}

// TASKS ACTUALLY RUN AT ONCE. Asserting only that a plan with 4 workers
// completes would pass against a scheduler that ignored the setting entirely.
func TestIndependentTasksRunConcurrently(t *testing.T) {
	probe := &concurrencyProbe{release: make(chan struct{})}
	plan := fanOutPlan(t, 4, 4)

	done := make(chan PlanReport, 1)
	go func() {
		done <- ExecutePlan(context.Background(), plan, []string{"read_file"}, probe.runner(), nil)
	}()

	// Wait for all four to be in flight together, which can only happen if they
	// genuinely overlap.
	deadline := time.After(5 * time.Second)
	for {
		probe.mu.Lock()
		peak := probe.peak
		probe.mu.Unlock()
		if peak >= 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("peak concurrency reached only %d of 4", peak)
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(probe.release)
	report := <-done
	if report.Succeeded != 4 {
		t.Fatalf("report = %+v", report)
	}
}

// DEPENDENCIES STILL HOLD. Concurrency may overlap independent work and must
// never start a task before what it waits on has finished.
func TestADependentTaskNeverStartsEarly(t *testing.T) {
	// a -> (b, c) -> d, with room to run everything at once if the scheduler
	// forgot the edges.
	plan := mustPlan(t, []any{
		task("a", "root"), task("b", "left", "a"), task("c", "right", "a"), task("d", "join", "b", "c"),
	}, map[string]any{"max_workers": float64(4)}, readOnlyLimits())

	var mu sync.Mutex
	finished := map[string]bool{}
	var violations []string

	ExecutePlan(context.Background(), plan, []string{"read_file"},
		func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
			mu.Lock()
			for _, dep := range req.Task.DependsOn {
				if !finished[dep] {
					violations = append(violations, req.Task.ID+" started before "+dep)
				}
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			finished[req.Task.ID] = true
			mu.Unlock()
			return TaskResult{Outcome: TaskSucceeded}, nil
		}, nil)

	if len(violations) > 0 {
		t.Fatalf("dependency order broken: %v", violations)
	}
}

// A DIAMOND OVERLAPS ITS MIDDLE. b and c are independent, so the scheduler must
// run them together — otherwise concurrency is wired but inert.
func TestADiamondsIndependentTasksOverlap(t *testing.T) {
	plan := mustPlan(t, []any{
		task("a", "root"), task("b", "left", "a"), task("c", "right", "a"), task("d", "join", "b", "c"),
	}, map[string]any{"max_workers": float64(4)}, readOnlyLimits())

	var mu sync.Mutex
	inFlight, peak := 0, 0
	ExecutePlan(context.Background(), plan, []string{"read_file"},
		func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return TaskResult{Outcome: TaskSucceeded}, nil
		}, nil)

	if peak < 2 {
		t.Fatalf("peak concurrency %d on a diamond; b and c are independent and must overlap", peak)
	}
}

// THE WORKER COUNT IS A CAP. A plan asking for 2 must never run 3, however many
// tasks are ready.
func TestTheWorkerCountIsRespected(t *testing.T) {
	probe := &concurrencyProbe{}
	report := ExecutePlan(context.Background(), fanOutPlan(t, 2, 8), []string{"read_file"}, probe.runner(), nil)
	if probe.peak > 2 {
		t.Fatalf("peak concurrency %d exceeded the 2 workers asked for", probe.peak)
	}
	if report.Succeeded != 8 {
		t.Fatalf("report = %+v", report)
	}
}

// THE MACHINE'S CAPACITY BOUNDS THE REQUEST, and both numbers are reported. A
// plan that asked for sixteen and ran six has not been given sixteen.
func TestTheEffectiveWorkerCountIsReported(t *testing.T) {
	if got := effectivePlanWorkers(1); got != 1 {
		t.Fatalf("one worker must stay one on every host, got %d", got)
	}
	if got := effectivePlanWorkers(maxPlanWorkers); got > machinePlanWorkers() {
		t.Fatalf("effective workers %d exceeds the machine's %d", got, machinePlanWorkers())
	}
	if machinePlanWorkers() < minPlanWorkers {
		t.Fatalf("machine workers %d fell below the floor", machinePlanWorkers())
	}

	report := ExecutePlan(context.Background(), fanOutPlan(t, maxPlanWorkers, 2), []string{"read_file"},
		func(context.Context, PlanTaskRequest) (TaskResult, error) {
			return TaskResult{Outcome: TaskSucceeded}, nil
		}, nil)
	if report.WorkersRequested != maxPlanWorkers {
		t.Fatalf("requested %d, want %d", report.WorkersRequested, maxPlanWorkers)
	}
	if report.Workers > report.WorkersRequested || report.Workers < 1 {
		t.Fatalf("effective workers %d is not a bound on %d", report.Workers, report.WorkersRequested)
	}
}

// A PANICKING TASK MUST NOT HANG THE PLAN. The slot has to come back, or the
// scheduler waits forever for a worker that will never report.
func TestAPanickingTaskFreesItsWorker(t *testing.T) {
	plan := fanOutPlan(t, 2, 4)
	var runs atomic.Int32
	done := make(chan PlanReport, 1)
	go func() {
		done <- ExecutePlan(context.Background(), plan, []string{"read_file"},
			func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
				if runs.Add(1) == 1 {
					panic("boom")
				}
				return TaskResult{Outcome: TaskSucceeded}, nil
			}, nil)
	}()
	select {
	case report := <-done:
		if report.Failed != 1 {
			t.Fatalf("report = %+v; the panicking task must be recorded as failed", report)
		}
		if report.Succeeded != 3 {
			t.Fatalf("report = %+v; the other tasks must still run", report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a panicking task hung the plan; its worker slot was never returned")
	}
}

// EVERY TASK IS HARVESTED before the report is assembled. A plan that reported
// while work was still in flight would report on tasks that had not finished.
func TestNoTaskIsLeftInFlightWhenThePlanReports(t *testing.T) {
	plan := fanOutPlan(t, 4, 12)
	var running atomic.Int32
	report := ExecutePlan(context.Background(), plan, []string{"read_file"},
		func(context.Context, PlanTaskRequest) (TaskResult, error) {
			running.Add(1)
			time.Sleep(2 * time.Millisecond)
			running.Add(-1)
			return TaskResult{Outcome: TaskSucceeded}, nil
		}, nil)
	if left := running.Load(); left != 0 {
		t.Fatalf("%d tasks still running when the plan reported", left)
	}
	if len(report.Tasks) != 12 || report.Succeeded != 12 {
		t.Fatalf("report = %+v", report)
	}
	// The report is in the PLAN's order, not completion order — a concurrent run
	// finishes out of order and the record must not.
	for index, id := range plan.Order() {
		if report.Tasks[index].ID != id {
			t.Fatalf("report order %v, want plan order %v", report.Tasks[index].ID, id)
		}
	}
}
