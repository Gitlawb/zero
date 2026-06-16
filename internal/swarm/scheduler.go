package swarm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// minScheduleInterval is the floor for a recurring spawn. Scheduling is opt-in
// and additive, but a tight loop could still flood a team's queue, so the
// shortest permitted interval is one second.
const minScheduleInterval = time.Second

// Schedule describes when a scheduled job fires. Scheduling is interval-based
// ("wakeup"): the job first fires after FirstDelay (or Every when FirstDelay is
// zero), then every Every interval, until MaxRuns successful spawns is reached
// or the job/scheduler is stopped. A daily "cron" time sets Daily with Hour/Minute
// (and Every=24h for display/validation); the run loop then recomputes the delay
// to the next local HH:MM each cycle so it holds across DST (see the
// swarm_schedule tool's daily_at handling).
type Schedule struct {
	// Every is the interval between fires. Required, must be >= minScheduleInterval.
	Every time.Duration
	// FirstDelay delays the first fire. Zero => the first fire happens after Every.
	FirstDelay time.Duration
	// MaxRuns bounds successful spawns. Zero => unbounded (until cancelled).
	MaxRuns int
	// Daily, when set, recomputes each fire as the next local Hour:Minute rather
	// than adding a fixed Every, so a wall-clock daily time does not drift across
	// DST transitions.
	Daily  bool
	Hour   int
	Minute int
}

func (sch Schedule) validate() error {
	if sch.Every < minScheduleInterval {
		return fmt.Errorf("swarm: schedule interval must be >= %s", minScheduleInterval)
	}
	if sch.MaxRuns < 0 {
		return errors.New("swarm: schedule max_runs must be >= 0")
	}
	return nil
}

// tickerFunc returns a channel that delivers one tick after d, plus a stop func
// that releases the underlying timer. Production uses realTicker; tests inject a
// controllable source. It mirrors time.NewTimer's one-shot semantics: the run
// loop requests a fresh ticker for each interval.
type tickerFunc func(d time.Duration) (<-chan time.Time, func())

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

// JobStatus is a read-only snapshot of a scheduled job for listing.
type JobStatus struct {
	ID        string
	Team      string
	AgentType string
	Task      string
	Every     time.Duration
	MaxRuns   int
	Runs      int
	// Skipped counts fires that did not spawn — either because the job's previous
	// spawn was still running (non-overlap) or a spawn attempt errored.
	Skipped int
}

// scheduledJob is one recurring spawn. Its goroutine owns the timing loop; mu
// guards the mutable counters and the last-spawned task id used for non-overlap.
type scheduledJob struct {
	id        string
	schedule  Schedule
	policy    Policy
	team      string
	agentType string
	task      string
	cwd       string

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	runs     int
	skipped  int
	lastTask string
}

func (j *scheduledJob) snapshot() JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return JobStatus{
		ID:        j.id,
		Team:      j.team,
		AgentType: j.agentType,
		Task:      j.task,
		Every:     j.schedule.Every,
		MaxRuns:   j.schedule.MaxRuns,
		Runs:      j.runs,
		Skipped:   j.skipped,
	}
}

// Scheduler fires recurring swarm spawns. It is opt-in: nothing runs unless a
// job is explicitly added via Add, and Close stops every job. Each job spawns a
// fresh member per interval through the same Swarm.Spawn path, so members
// inherit the recorded policy and are bounded by the team's slot cap and queue.
type Scheduler struct {
	sw        *Swarm
	newTicker tickerFunc
	// now is the wall clock used to recompute a daily job's next local fire each
	// cycle (so HH:MM holds across DST). Defaults to time.Now; tests override it.
	now func() time.Time

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	jobs   map[string]*scheduledJob
	closed bool
	seq    atomic.Uint64
	wg     sync.WaitGroup
}

// newScheduler builds a Scheduler bound to sw. Its context derives from the
// Swarm's base context, so closing the Swarm also stops every scheduled job.
func newScheduler(sw *Swarm) *Scheduler {
	ctx, cancel := context.WithCancel(sw.baseCtx)
	return &Scheduler{
		sw:        sw,
		newTicker: realTicker,
		now:       time.Now,
		ctx:       ctx,
		cancel:    cancel,
		jobs:      map[string]*scheduledJob{},
	}
}

// clock returns the scheduler's wall clock, defaulting to time.Now.
func (s *Scheduler) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Add registers a recurring spawn and starts its timing loop. It validates the
// schedule and the agent type up front (fail fast) so a bad job never starts.
// The returned id identifies the job for List/Cancel.
func (s *Scheduler) Add(pol Policy, teamName, agentType, task, cwd string, sch Schedule) (string, error) {
	if err := sch.validate(); err != nil {
		return "", err
	}
	if _, err := s.sw.registry.Lookup(agentType); err != nil {
		return "", err
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return "", errors.New("swarm: schedule requires a task")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("swarm: scheduler is closed")
	}
	// A scheduler whose parent context is already canceled (e.g. after
	// Swarm.Close) is effectively closed: a new job's loop would exit on the
	// first select, so reject Add rather than reporting a job that never runs.
	select {
	case <-s.ctx.Done():
		return "", errors.New("swarm: scheduler is closed")
	default:
	}
	id := fmt.Sprintf("sched-%d", s.seq.Add(1))
	ctx, cancel := context.WithCancel(s.ctx)
	job := &scheduledJob{
		id:        id,
		schedule:  sch,
		policy:    pol,
		team:      sanitizeName(teamName),
		agentType: agentType,
		task:      task,
		cwd:       cwd,
		ctx:       ctx,
		cancel:    cancel,
	}
	s.jobs[id] = job
	s.wg.Add(1)
	go s.run(job)
	return id, nil
}

// Cancel stops a scheduled job by id. It reports whether a job was found.
func (s *Scheduler) Cancel(id string) bool {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if ok {
		delete(s.jobs, id)
	}
	s.mu.Unlock()
	if ok {
		job.cancel()
	}
	return ok
}

// List returns a snapshot of every active scheduled job.
func (s *Scheduler) List() []JobStatus {
	s.mu.Lock()
	jobs := make([]*scheduledJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.Unlock()
	out := make([]JobStatus, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.snapshot())
	}
	return out
}

// Close cancels every job and waits for their loops to exit. Safe to call more
// than once.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

// run is one job's timing loop. It requests a fresh one-shot ticker per interval
// and fires until cancelled or MaxRuns is reached.
func (s *Scheduler) run(job *scheduledJob) {
	defer s.wg.Done()
	defer s.forget(job.id)

	delay := job.schedule.FirstDelay
	if delay <= 0 {
		delay = job.schedule.Every
	}
	for {
		ch, stop := s.newTicker(delay)
		select {
		case <-job.ctx.Done():
			stop()
			return
		case <-ch:
			stop()
		}
		// A tick and a cancel can be ready together; the select above may pick the
		// tick, so re-check cancellation before firing to avoid one extra spawn
		// after Cancel/Close.
		select {
		case <-job.ctx.Done():
			return
		default:
		}
		// Daily jobs recompute the delay to the next local HH:MM each cycle so the
		// wall-clock time holds across DST; interval jobs use the fixed Every.
		if job.schedule.Daily {
			delay = nextDailyDelay(s.clock(), job.schedule.Hour, job.schedule.Minute)
		} else {
			delay = job.schedule.Every
		}
		if !s.fireIfIdle(job) {
			continue
		}
		runs := job.incRuns()
		if max := job.schedule.MaxRuns; max > 0 && runs >= max {
			return
		}
	}
}

// fireIfIdle spawns a fresh member unless the job's previous spawn is still
// running (non-overlap). It returns whether a spawn occurred. A spawn error is
// treated like a skip so a transient failure never tears down the loop.
func (s *Scheduler) fireIfIdle(job *scheduledJob) bool {
	job.mu.Lock()
	last := job.lastTask
	job.mu.Unlock()

	if last != "" {
		if t, ok := s.sw.coord.Get(last); ok && !t.Status.terminal() {
			job.incSkipped()
			return false
		}
	}

	id, err := s.sw.Spawn(job.policy, job.team, job.agentType, job.task, job.cwd)
	if err != nil {
		job.incSkipped()
		return false
	}
	job.mu.Lock()
	job.lastTask = id
	job.mu.Unlock()
	return true
}

func (j *scheduledJob) incRuns() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.runs++
	return j.runs
}

func (j *scheduledJob) incSkipped() {
	j.mu.Lock()
	j.skipped++
	j.mu.Unlock()
}

func (s *Scheduler) forget(id string) {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
}

// nextDailyDelay returns the duration from now until the next occurrence of the
// given local hour:minute (today if still ahead, otherwise tomorrow). It backs
// the swarm_schedule tool's daily_at ("cron"-style) option.
func nextDailyDelay(now time.Time, hour, minute int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		// Roll to the same wall-clock time on the next calendar day via day+1
		// (not Add(24h)): time.Date normalizes the date and applies the correct
		// local offset, so the HH:MM holds across DST (a spring-forward/fall-back
		// day is 23h/25h, and a fixed 24h would fire an hour early/late).
		next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, now.Location())
	}
	return next.Sub(now)
}
