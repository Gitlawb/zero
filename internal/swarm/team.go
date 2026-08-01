package swarm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// defaultMaxTeamSize bounds concurrently-running members per team. Spawns past
// the cap queue and launch as slots free up (mirrors daemon.Pool's bounded,
// on-demand worker model).
const defaultMaxTeamSize = 8

// maxMemberRestarts bounds automatic relaunches of a member that exits with a
// temporary error, mirroring daemon.Pool's bounded backoff retries.
const maxMemberRestarts = 2

// Options configures a Swarm.
type Options struct {
	// BaseDir is the mailbox root (required).
	BaseDir string
	// Launcher turns specs into running members (required).
	Launcher MemberLauncher
	// Registry is the agent roster; nil => a fresh built-in registry.
	Registry *Registry
	// Coordinator tracks tasks; nil => a fresh coordinator.
	Coordinator *Coordinator
	// MaxTeamSize caps concurrent members per team; 0 => defaultMaxTeamSize.
	MaxTeamSize int
	// Definitions are user-defined agents to add to the roster (extending or
	// overriding the built-ins). Empty agent types are rejected.
	Definitions []Definition
	// Context is the parent context for all members; members outlive the
	// per-call tool context (a spawn returns immediately). nil => Background.
	// Close cancels the derived context, stopping every member.
	Context context.Context
}

// Policy is the orchestrator's live model + permission mode at spawn time. Every
// member inherits it: a member never runs on a different model than requested,
// nor with a more permissive mode than its parent. It is passed per call (not
// stored on the Swarm) because the orchestrator's model/mode can change
// mid-session (e.g. an escalate-model).
type Policy struct {
	Model          string
	PermissionMode string
}

// Swarm is the façade the swarm tools call. It owns the agent roster, the task
// coordinator, the mailbox, and the live teams, and enforces that every member
// inherits the orchestrator's model + permission mode.
type Swarm struct {
	registry    *Registry
	coord       *Coordinator
	mailbox     *Mailbox
	launcher    MemberLauncher
	maxTeamSize int

	baseCtx context.Context
	cancel  context.CancelFunc

	// lifecycleMu guards closed and orders admission against shutdown. It is held
	// only for the flag check plus the lifecycleWork increment — never across a
	// launcher call, which would deadlock Close (see beginLifecycleAdmission).
	lifecycleMu sync.RWMutex
	closed      bool
	// lifecycleWork counts admitted-but-unfinished lifecycle operations (spawn,
	// handoff, adoption, retry, queue drain). Close waits it out so no admitted
	// operation is still touching the swarm when Close returns.
	lifecycleWork sync.WaitGroup
	watchers      sync.WaitGroup
	closeOnce     sync.Once

	mu        sync.Mutex
	teams     map[string]*Team
	taskCwd   map[string]string // taskID -> cwd, for handoff/adoption relaunch
	scheduler *Scheduler        // lazily created by Scheduler(); nil until first use
	idSeq     atomic.Uint64
}

// Team is a named set of concurrently-running members with a bounded slot count
// and a FIFO queue for overflow spawns.
type Team struct {
	Name string

	mu      sync.Mutex
	members map[string]*Member
	queue   []MemberSpec
	running int
	maxSize int
}

// Member is one running agent within a team.
type Member struct {
	ID        string
	AgentType string
	TaskID    string
	handle    MemberHandle
	restarts  int
}

// New validates options and returns a Swarm.
func New(opts Options) (*Swarm, error) {
	if opts.Launcher == nil {
		return nil, errors.New("swarm: a MemberLauncher is required")
	}
	mb, err := NewMailbox(opts.BaseDir)
	if err != nil {
		return nil, err
	}
	registry := opts.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	for _, def := range opts.Definitions {
		if err := registry.Register(def); err != nil {
			return nil, err
		}
	}
	coord := opts.Coordinator
	if coord == nil {
		coord = NewCoordinator()
	}
	maxTeam := opts.MaxTeamSize
	if maxTeam <= 0 {
		maxTeam = defaultMaxTeamSize
	}
	parent := opts.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Swarm{
		registry:    registry,
		coord:       coord,
		mailbox:     mb,
		launcher:    opts.Launcher,
		maxTeamSize: maxTeam,
		baseCtx:     ctx,
		cancel:      cancel,
		teams:       map[string]*Team{},
		taskCwd:     map[string]string{},
	}, nil
}

// Close cancels every running member's context and releases resources. It is
// safe to call more than once. It closes lifecycle admission before canceling
// members, fails queued tasks, and waits for every member watcher to exit.
func (s *Swarm) Close() {
	s.closeOnce.Do(func() {
		// Closing admission and cancelling are separate steps on purpose. The flag
		// flips under lifecycleMu so no further work is admitted, and the lock is
		// released BEFORE cancelling: a member already being launched holds no lock
		// here, but it may be blocked inside MemberLauncher.Launch waiting for the
		// context this cancel provides, and it can only release its admission ticket
		// once that cancellation lands.
		s.lifecycleMu.Lock()
		s.closed = true
		s.mu.Lock()
		sched := s.scheduler
		s.mu.Unlock()
		s.lifecycleMu.Unlock()

		if s.cancel != nil {
			s.cancel()
		}
		if sched != nil {
			sched.Close()
		}
		// Wait for every already-admitted lifecycle operation (spawn, handoff,
		// adoption, retry, queue drain) to finish BEFORE sweeping queues below.
		// Such work can create teams as well as append to their queues, so snapshot
		// the teams only after every ticket is back. At that point no team or queue
		// can gain a new entry, and the sweep cannot miss late-admitted work.
		s.lifecycleWork.Wait()
		s.mu.Lock()
		teams := make([]*Team, 0, len(s.teams))
		for _, team := range s.teams {
			teams = append(teams, team)
		}
		s.mu.Unlock()
		for _, team := range teams {
			for _, spec := range team.clearQueue() {
				_ = s.coord.Fail(spec.TaskID, ErrSwarmClosed.Error())
			}
		}
		// Every watcher is added while its admitting lifecycle ticket is still held,
		// so lifecycleWork.Wait above also orders every Add before this Wait. The
		// watcher itself can outlive that ticket, which is why both waits are needed.
		s.watchers.Wait()
	})
}

// Scheduler returns the swarm's recurring-spawn scheduler, creating it on first
// use. Scheduling is opt-in: until a job is added the scheduler does nothing.
//
// The post-close handoff between this method and Close relies on the ORDER in
// which each reads/writes s.scheduler and s.closed, not on holding one lock across
// both: Close takes lifecycleMu.Lock() (excluding this RLock), sets closed, reads
// whatever s.scheduler currently is, and only THEN releases lifecycleMu and calls
// sched.Close() on what it captured. So a call here either:
//   - runs entirely before Close's write lock — sees closed == false, and the
//     scheduler it creates/returns is exactly the one Close's later snapshot will
//     capture and close; or
//   - runs entirely after Close's write lock — sees closed == true (memory model:
//     the RLock/Lock pair orders the read after Close's write) and closes the
//     scheduler itself here, because Close already captured its own snapshot
//     (possibly nil, if this call is what first creates one) and will not look
//     again.
//
// Either way exactly one of the two call sites closes the scheduler that ends up
// installed, and neither can observe a "created after close but never closed"
// scheduler — that would require reading closed == false here while also reading
// a scheduler Close had already passed over, which the lock ordering rules out.
func (s *Swarm) Scheduler() *Scheduler {
	s.lifecycleMu.RLock()
	closed := s.closed
	s.mu.Lock()
	if s.scheduler == nil {
		s.scheduler = newScheduler(s)
	}
	sched := s.scheduler
	s.mu.Unlock()
	s.lifecycleMu.RUnlock()
	if closed {
		sched.Close()
	}
	return sched
}

// ErrSwarmClosed reports lifecycle work submitted after shutdown begins.
var ErrSwarmClosed = errors.New("swarm: closed")

// rememberCwd records a task's working dir so a handoff/adoption relaunch keeps it.
func (s *Swarm) rememberCwd(taskID, cwd string) {
	s.mu.Lock()
	s.taskCwd[taskID] = cwd
	s.mu.Unlock()
}

// cwdFor returns a task's recorded working dir ("" if none).
func (s *Swarm) cwdFor(taskID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taskCwd[taskID]
}

// Registry exposes the roster (for tool listing / user-defined registration).
func (s *Swarm) Registry() *Registry { return s.registry }

// Coordinator exposes the task registry (for status/collect).
func (s *Swarm) Coordinator() *Coordinator { return s.coord }

// Mailbox exposes the message store (for send/inbox tools).
func (s *Swarm) Mailbox() *Mailbox { return s.mailbox }

// team returns the named team, creating it on first use.
func (s *Swarm) team(name string) *Team {
	key := sanitizeName(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.teams[key]
	if !ok {
		t = &Team{Name: key, members: map[string]*Member{}, maxSize: s.maxTeamSize}
		s.teams[key] = t
	}
	return t
}

// nextID mints a stable, unique member/task id within the process.
func (s *Swarm) nextID(agentType string) string {
	n := s.idSeq.Add(1)
	return sanitizeName(agentType) + "-" + strconv.FormatUint(n, 10)
}

// resolveModel folds the "inherit" sentinel into the orchestrator's model so a
// launched member never silently runs on a different model.
func resolveModel(pol Policy, def Definition) string {
	if def.Model == "" || def.Model == modelInherit {
		return pol.Model
	}
	return def.Model
}

// resolvePermissionMode never widens authority: a member uses its definition's
// mode only if it is not more permissive than the orchestrator's. Unknown modes
// fall back to the orchestrator's mode (fail closed).
func resolvePermissionMode(pol Policy, def Definition) string {
	if def.PermissionMode == "" {
		return pol.PermissionMode
	}
	if permissionRank(def.PermissionMode) > permissionRank(pol.PermissionMode) {
		// Definition asks for more than the parent has — clamp to the parent.
		return pol.PermissionMode
	}
	return def.PermissionMode
}

// Runtime permission modes (mirror internal/agent.PermissionMode without
// importing it, to avoid an import cycle). These are the actual values that flow
// through tools.RunOptions.PermissionMode — NOT the TUI display names.
const (
	permissionModeAsk       = "ask"        // prompts for every tool (most restrictive)
	permissionModeSpecDraft = "spec-draft" // spec-drafting only
	permissionModeAuto      = "auto"       // auto-approve low-risk
	permissionModeUnsafe    = "unsafe"     // approve everything (most permissive)
)

// permissionRank orders permission modes from least to most permissive so the
// swarm can clamp a member to no more than its parent. Unknown/empty modes rank
// as the strictest (0) so they never accidentally widen access. spec-draft
// (which scopes a child to spec tooling) ranks below ask: it never grants more
// authority than prompting-for-everything, so a spec-draft parent clamps members
// hardest among the known modes.
func permissionRank(mode string) int {
	switch strings.TrimSpace(mode) {
	case permissionModeSpecDraft:
		return 1
	case permissionModeAsk:
		return 2
	case permissionModeAuto:
		return 3
	case permissionModeUnsafe:
		return 4
	default:
		return 0
	}
}

// admit reserves a running slot for a spec, or queues it. It reports whether the
// caller should launch immediately.
func (t *Team) admit(spec MemberSpec) (launchNow bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running < t.maxSize {
		t.running++
		return true
	}
	t.queue = append(t.queue, spec)
	return false
}

// onExit releases a slot after a member exits and returns the next queued spec
// (already re-reserving its slot) if one is waiting.
func (t *Team) onExit() (MemberSpec, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running > 0 {
		t.running--
	}
	if len(t.queue) > 0 && t.running < t.maxSize {
		next := t.queue[0]
		t.queue = t.queue[1:]
		t.running++
		return next, true
	}
	return MemberSpec{}, false
}

func (t *Team) releaseSlot() {
	t.mu.Lock()
	if t.running > 0 {
		t.running--
	}
	t.mu.Unlock()
}

func (t *Team) clearQueue() []MemberSpec {
	t.mu.Lock()
	defer t.mu.Unlock()
	queued := t.queue
	t.queue = nil
	return queued
}

func (t *Team) addMember(m *Member) {
	t.mu.Lock()
	t.members[m.ID] = m
	t.mu.Unlock()
}

func (t *Team) removeMember(id string) {
	t.mu.Lock()
	delete(t.members, id)
	t.mu.Unlock()
}

// liveAgents returns the set of agent ids that currently have a live member, so
// orphan detection can tell which tasks have lost their owner.
func (t *Team) liveAgents() map[string]struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	live := make(map[string]struct{}, len(t.members)+len(t.queue))
	for _, m := range t.members {
		live[m.ID] = struct{}{}
	}
	// Queued specs are over the concurrency cap but still owned and about to launch
	// as slots free; count them live so orphan adoption never re-dispatches (and
	// thus double-executes) a task whose member is merely waiting for a slot.
	for _, spec := range t.queue {
		live[spec.ID] = struct{}{}
	}
	return live
}

// QueueDepth reports how many specs are waiting for a slot (status/tests).
func (t *Team) QueueDepth() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.queue)
}

// Running reports how many members are currently occupying a slot.
func (t *Team) Running() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// ErrMemberTemporary marks a member failure as transient (worker died, transient
// I/O) so the swarm relaunches it within maxMemberRestarts, mirroring daemon
// EXIT_TEMPFAIL semantics. Permanent failures are not retried.
var ErrMemberTemporary = errors.New("swarm: temporary member failure")

// isRetryable reports whether a member error should trigger a bounded relaunch.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrMemberTemporary) {
		return true
	}
	var t interface{ Temporary() bool }
	if errors.As(err, &t) {
		return t.Temporary()
	}
	return false
}

// buildSpec resolves a definition + task into a launchable spec under the
// orchestrator's policy. memberID identifies the running agent; taskID is the
// coordinator task it serves (equal for a fresh spawn, distinct after adoption).
func (s *Swarm) buildSpec(pol Policy, memberID, taskID, team string, def Definition, task, cwd string) MemberSpec {
	prompt := ""
	if def.SystemPrompt != nil {
		prompt = def.SystemPrompt(PromptContext{Team: team, Task: task})
	}
	return MemberSpec{
		ID:             memberID,
		TaskID:         taskID,
		AgentType:      def.AgentType,
		Team:           team,
		Task:           task,
		Cwd:            cwd,
		Model:          resolveModel(pol, def),
		PermissionMode: resolvePermissionMode(pol, def),
		SystemPrompt:   prompt,
	}
}

// memberError wraps a member's failure for coordinator recording.
func memberError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}

// nowRFC3339 is a tiny helper for mailbox handoff notes.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
