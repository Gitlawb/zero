package swarm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// controllableLauncher records every launched spec and lets a test control each
// member's outcome and (optionally) gate completion until a channel is closed.
type controllableLauncher struct {
	mu        sync.Mutex
	specs     []MemberSpec
	attempts  map[string]int
	gate      chan struct{} // nil => members return immediately
	result    func(spec MemberSpec, attempt int) (MemberResult, error)
	launchErr error
}

func newLauncher(result func(MemberSpec, int) (MemberResult, error)) *controllableLauncher {
	return &controllableLauncher{attempts: map[string]int{}, result: result}
}

func (l *controllableLauncher) Launch(ctx context.Context, spec MemberSpec) (MemberHandle, error) {
	l.mu.Lock()
	if l.launchErr != nil {
		err := l.launchErr
		l.mu.Unlock()
		return nil, err
	}
	l.specs = append(l.specs, spec)
	l.attempts[spec.ID]++
	attempt := l.attempts[spec.ID]
	gate := l.gate
	result := l.result
	l.mu.Unlock()

	h := &funcHandle{id: spec.ID, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				h.err = ctx.Err()
				return
			}
		}
		if result != nil {
			h.res, h.err = result(spec, attempt)
		}
	}()
	return h, nil
}

func (l *controllableLauncher) recorded() []MemberSpec {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]MemberSpec, len(l.specs))
	copy(out, l.specs)
	return out
}

func (l *controllableLauncher) attemptCount(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attempts[id]
}

func newSwarmFor(t *testing.T, l MemberLauncher) *Swarm {
	t.Helper()
	sw, err := New(Options{BaseDir: t.TempDir(), Launcher: l, MaxTeamSize: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(sw.Close)
	return sw
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func okFor(spec MemberSpec, _ int) (MemberResult, error) {
	return MemberResult{Result: "ok:" + spec.Task, SessionID: "sess-" + spec.ID}, nil
}

func TestSpawnCompletes(t *testing.T) {
	l := newLauncher(okFor)
	sw := newSwarmFor(t, l)
	id, err := sw.Spawn(Policy{Model: "m"}, "team", "teammate", "build widget", "/work")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitFor(t, "task done", func() bool {
		task, ok := sw.Coordinator().Get(id)
		return ok && task.Status == StatusDone
	})
	task, _ := sw.Coordinator().Get(id)
	if task.Result != "ok:build widget" {
		t.Fatalf("result = %q", task.Result)
	}
}

func TestSpawnInheritsPolicy(t *testing.T) {
	l := newLauncher(okFor)
	sw := newSwarmFor(t, l)
	_, err := sw.Spawn(Policy{Model: "orch-model", PermissionMode: permissionModeAuto}, "team", "teammate", "task", "/cwd")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitFor(t, "spec recorded", func() bool { return len(l.recorded()) == 1 })
	spec := l.recorded()[0]
	if spec.Model != "orch-model" {
		t.Fatalf("member model = %q, want inherited orch-model", spec.Model)
	}
	if spec.PermissionMode != permissionModeAuto {
		t.Fatalf("member permission mode = %q, want inherited auto", spec.PermissionMode)
	}
	if spec.Cwd != "/cwd" {
		t.Fatalf("member cwd = %q, want /cwd", spec.Cwd)
	}
	if spec.SystemPrompt == "" {
		t.Fatal("member should carry a resolved system prompt")
	}
}

func TestConcurrencyCapAndQueueDrains(t *testing.T) {
	gate := make(chan struct{})
	l := newLauncher(okFor)
	l.gate = gate
	sw := newSwarmFor(t, l) // MaxTeamSize 2
	pol := Policy{Model: "m"}
	for i := 0; i < 5; i++ {
		if _, err := sw.Spawn(pol, "team", "teammate", "task", ""); err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
	}
	team := sw.team("team")
	if team.Running() != 2 || team.QueueDepth() != 3 {
		t.Fatalf("cap not enforced: running=%d queue=%d, want 2/3", team.Running(), team.QueueDepth())
	}
	// Release everyone; the queue should drain one-per-slot until all are done.
	close(gate)
	waitFor(t, "all tasks done", func() bool { return sw.Coordinator().Summarize().Done == 5 })
	if team.Running() != 0 || team.QueueDepth() != 0 {
		t.Fatalf("after drain running=%d queue=%d, want 0/0", team.Running(), team.QueueDepth())
	}
	if got := len(l.recorded()); got != 5 {
		t.Fatalf("launched %d members, want 5", got)
	}
}

func TestRetryOnTemporaryError(t *testing.T) {
	l := newLauncher(func(spec MemberSpec, attempt int) (MemberResult, error) {
		if attempt < 3 {
			return MemberResult{}, ErrMemberTemporary
		}
		return MemberResult{Result: "recovered"}, nil
	})
	sw := newSwarmFor(t, l)
	id, _ := sw.Spawn(Policy{}, "team", "teammate", "task", "")
	waitFor(t, "task recovered", func() bool {
		task, ok := sw.Coordinator().Get(id)
		return ok && task.Status == StatusDone
	})
	if got := l.attemptCount(id); got != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + 2 retries)", got)
	}
	task, _ := sw.Coordinator().Get(id)
	if task.Result != "recovered" {
		t.Fatalf("result = %q, want recovered", task.Result)
	}
}

func TestRetryExhaustionFails(t *testing.T) {
	l := newLauncher(func(MemberSpec, int) (MemberResult, error) {
		return MemberResult{}, ErrMemberTemporary
	})
	sw := newSwarmFor(t, l)
	id, _ := sw.Spawn(Policy{}, "team", "teammate", "task", "")
	waitFor(t, "task failed", func() bool {
		task, ok := sw.Coordinator().Get(id)
		return ok && task.Status == StatusFailed
	})
	if got := l.attemptCount(id); got != maxMemberRestarts+1 {
		t.Fatalf("attempts = %d, want %d", got, maxMemberRestarts+1)
	}
}

func TestPermanentErrorNoRetry(t *testing.T) {
	l := newLauncher(func(MemberSpec, int) (MemberResult, error) {
		return MemberResult{}, errPlain("hard failure")
	})
	sw := newSwarmFor(t, l)
	id, _ := sw.Spawn(Policy{}, "team", "teammate", "task", "")
	waitFor(t, "task failed", func() bool {
		task, ok := sw.Coordinator().Get(id)
		return ok && task.Status == StatusFailed
	})
	if got := l.attemptCount(id); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on permanent error)", got)
	}
	task, _ := sw.Coordinator().Get(id)
	if task.Err == "" {
		t.Fatal("failed task should record an error message")
	}
}

func TestLifecycleAdmissionRejectedAfterClose(t *testing.T) {
	l := newLauncher(okFor)
	sw := newSwarmFor(t, l)
	sw.Close()

	if _, err := sw.Spawn(Policy{}, "team", "teammate", "task", ""); !errors.Is(err, ErrSwarmClosed) {
		t.Fatalf("Spawn after Close error = %v, want ErrSwarmClosed", err)
	}
	if _, err := sw.Handoff(Policy{}, "team", "task", "teammate", "note"); !errors.Is(err, ErrSwarmClosed) {
		t.Fatalf("Handoff after Close error = %v, want ErrSwarmClosed", err)
	}
	if _, err := sw.AdoptOrphans(Policy{}, "team", "teammate"); !errors.Is(err, ErrSwarmClosed) {
		t.Fatalf("AdoptOrphans after Close error = %v, want ErrSwarmClosed", err)
	}
	if got := len(l.recorded()); got != 0 {
		t.Fatalf("launches after Close = %d, want 0", got)
	}
}

func TestCloseDoesNotLaunchQueuedMembers(t *testing.T) {
	gate := make(chan struct{})
	l := newLauncher(okFor)
	l.gate = gate
	sw := newSwarmFor(t, l)

	var ids []string
	for i := 0; i < 3; i++ {
		id, err := sw.Spawn(Policy{}, "team", "teammate", "task", "")
		if err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	if got := len(l.recorded()); got != 2 {
		t.Fatalf("initial launches = %d, want 2 with one queued", got)
	}

	sw.Close()
	if got := len(l.recorded()); got != 2 {
		t.Fatalf("launches after Close = %d, want queued member not launched", got)
	}
	for _, id := range ids {
		task, ok := sw.Coordinator().Get(id)
		if !ok || !task.Status.terminal() {
			t.Fatalf("task %s after Close = %+v, want terminal", id, task)
		}
	}
}

func TestClosePreventsMemberRetry(t *testing.T) {
	started := make(chan struct{}, maxMemberRestarts+1)
	release := make(chan struct{})
	l := FuncLauncher{Run: func(context.Context, MemberSpec) (MemberResult, error) {
		started <- struct{}{}
		<-release
		return MemberResult{}, ErrMemberTemporary
	}}
	sw := newSwarmFor(t, l)
	_, err := sw.Spawn(Policy{}, "team", "teammate", "task", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("initial member did not start")
	}

	closed := make(chan struct{})
	go func() {
		sw.Close()
		close(closed)
	}()
	waitFor(t, "swarm closed state", func() bool {
		sw.lifecycleMu.RLock()
		defer sw.lifecycleMu.RUnlock()
		return sw.closed
	})
	close(release)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after retryable member exit")
	}
	select {
	case <-started:
		t.Fatal("member retried after Close")
	default:
	}
}

func TestCloseWaitsForMemberWatchers(t *testing.T) {
	release := make(chan struct{})
	l := FuncLauncher{Run: func(context.Context, MemberSpec) (MemberResult, error) {
		<-release
		return MemberResult{Result: "done"}, nil
	}}
	sw := newSwarmFor(t, l)
	id, err := sw.Spawn(Policy{}, "team", "teammate", "task", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitFor(t, "task running", func() bool {
		task, ok := sw.Coordinator().Get(id)
		return ok && task.Status == StatusRunning
	})

	const callers = 3
	closing := make(chan struct{}, callers)
	closed := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func() {
			closing <- struct{}{}
			sw.Close()
			closed <- struct{}{}
		}()
	}
	for i := 0; i < callers; i++ {
		<-closing
	}
	select {
	case <-closed:
		t.Fatal("a Close caller returned before the member watcher exited")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for i := 0; i < callers; i++ {
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("all Close callers did not return after the member watcher exited")
		}
	}
}

func TestHandoffDeliversNoteAndRetiresOriginal(t *testing.T) {
	gate := make(chan struct{})
	l := newLauncher(okFor)
	l.gate = gate // keep the original member running so it is non-terminal
	sw := newSwarmFor(t, l)
	pol := Policy{Model: "m"}
	origID, _ := sw.Spawn(pol, "team", "teammate", "original task", "/w")
	waitFor(t, "original running", func() bool {
		task, ok := sw.Coordinator().Get(origID)
		return ok && task.Status == StatusRunning
	})

	newID, err := sw.Handoff(pol, "team", origID, "subagent", "please continue")
	if err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	// Original retired.
	orig, _ := sw.Coordinator().Get(origID)
	if orig.Status != StatusHandedOff {
		t.Fatalf("original status = %v, want handed-off", orig.Status)
	}
	// Note delivered to the new member's inbox.
	msgs, err := sw.Mailbox().ReadAndConsume("team", newID)
	if err != nil {
		t.Fatalf("read new inbox: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "please continue" || msgs[0].Type != "handoff" {
		t.Fatalf("handoff note not delivered: %+v", msgs)
	}
	// The new member carries the handoff note in its task and preserves cwd.
	close(gate)
	waitFor(t, "spec for new member", func() bool {
		for _, s := range l.recorded() {
			if s.ID == newID {
				return true
			}
		}
		return false
	})
	for _, s := range l.recorded() {
		if s.ID == newID {
			if s.Cwd != "/w" {
				t.Fatalf("handoff lost cwd: %q", s.Cwd)
			}
		}
	}
	// A handoff of an already-terminal task is rejected.
	waitFor(t, "new task done", func() bool {
		task, ok := sw.Coordinator().Get(newID)
		return ok && task.Status == StatusDone
	})
	if _, err := sw.Handoff(pol, "team", newID, "teammate", "again"); err == nil {
		t.Fatal("handoff of a terminal task must fail")
	}
}

func TestAdoptOrphans(t *testing.T) {
	l := newLauncher(okFor)
	sw := newSwarmFor(t, l)
	// Simulate a crashed member: a running task in the coordinator whose owning
	// agent has no live member in the team.
	if _, err := sw.Coordinator().Register("orphan-1", "ghost", "team", "stranded work"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_ = sw.Coordinator().SetStatus("orphan-1", StatusRunning)

	adopted, err := sw.AdoptOrphans(Policy{Model: "m"}, "team", "subagent")
	if err != nil {
		t.Fatalf("AdoptOrphans: %v", err)
	}
	if len(adopted) != 1 || adopted[0] != "orphan-1" {
		t.Fatalf("adopted = %v, want [orphan-1]", adopted)
	}
	// The orphan is relaunched under a fresh agent and completes.
	waitFor(t, "orphan completed", func() bool {
		task, ok := sw.Coordinator().Get("orphan-1")
		return ok && task.Status == StatusDone
	})
	task, _ := sw.Coordinator().Get("orphan-1")
	if task.AgentID == "ghost" {
		t.Fatal("orphan should be reassigned to a new agent")
	}
	// A second adoption finds nothing (the task now has a live/owned outcome).
	again, _ := sw.AdoptOrphans(Policy{}, "team", "subagent")
	if len(again) != 0 {
		t.Fatalf("second adoption = %v, want none", again)
	}
}

func TestLiveAgentsIncludesQueuedSpecs(t *testing.T) {
	// H1 regression: a spec queued over the concurrency cap is owned and about to
	// launch, so it must count as a live agent. Otherwise AdoptOrphans sees its
	// task as orphaned and re-dispatches it — double-executing the same task.
	team := &Team{Name: "t", members: map[string]*Member{}, maxSize: 1}
	if !team.admit(MemberSpec{ID: "a1", TaskID: "t1"}) {
		t.Fatal("first spec should take the only slot and launch immediately")
	}
	team.addMember(&Member{ID: "a1"})
	if team.admit(MemberSpec{ID: "a2", TaskID: "t2"}) {
		t.Fatal("second spec is over the cap and should queue, not launch")
	}
	live := team.liveAgents()
	if _, ok := live["a1"]; !ok {
		t.Error("running member a1 missing from liveAgents")
	}
	if _, ok := live["a2"]; !ok {
		t.Error("queued spec a2 missing from liveAgents — would be double-dispatched by AdoptOrphans")
	}
}

func TestCollectScopesToTeam(t *testing.T) {
	l := newLauncher(okFor)
	sw := newSwarmFor(t, l)
	a, _ := sw.Spawn(Policy{}, "alpha", "teammate", "ta", "")
	_, _ = sw.Spawn(Policy{}, "beta", "teammate", "tb", "")
	waitFor(t, "alpha done", func() bool {
		task, ok := sw.Coordinator().Get(a)
		return ok && task.Status == StatusDone
	})
	collected := sw.Collect("alpha")
	if len(collected) != 1 || collected[0].Team != "alpha" {
		t.Fatalf("Collect(alpha) = %+v, want one alpha task", collected)
	}
}

func TestFuncLauncherRecoversPanic(t *testing.T) {
	// A panic inside a member's Run must surface as that member's error, never
	// escape the goroutine and crash the orchestrator.
	l := FuncLauncher{Run: func(context.Context, MemberSpec) (MemberResult, error) {
		panic("boom in member")
	}}
	h, err := l.Launch(context.Background(), MemberSpec{ID: "m1"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	_, err = h.Wait()
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic must surface as a member error, got %v", err)
	}
}

func TestSpawnUnknownAgentType(t *testing.T) {
	sw := newSwarmFor(t, newLauncher(okFor))
	if _, err := sw.Spawn(Policy{}, "team", "does-not-exist", "task", ""); err == nil {
		t.Fatal("Spawn with unknown agent type must error")
	}
}
