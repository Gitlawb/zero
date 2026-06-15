package swarm

import (
	"context"
	"errors"
	"fmt"
)

// MemberSpec is the fully-resolved instruction to launch one member. The swarm
// builds it from a Definition + task; a MemberLauncher turns it into a running
// agent. PermissionMode/Model carry the orchestrator's policy so the launcher
// can never grant a member more authority than its parent.
type MemberSpec struct {
	// ID identifies the running member (its agent id). For a freshly spawned
	// member this equals TaskID; after an orphan adoption a new agent (new ID)
	// takes over the original TaskID.
	ID             string
	TaskID         string
	AgentType      string
	Team           string
	Task           string
	Cwd            string
	Model          string // resolved (never the "inherit" sentinel)
	PermissionMode string
	SystemPrompt   string
}

// MemberResult is what a finished member returns.
type MemberResult struct {
	Result    string
	SessionID string
}

// MemberHandle is a running member. Wait blocks until the member exits and
// returns its result or error; ID identifies the member.
type MemberHandle interface {
	ID() string
	Wait() (MemberResult, error)
}

// MemberLauncher turns a MemberSpec into a running member. Production wires two
// implementations behind this seam — one over internal/specialist.Executor
// (direct) and one over internal/daemon.Pool (when a daemon is running) — while
// tests inject a fake. Keeping the seam here means the swarm core has no
// compile-time dependency on either heavy subsystem.
type MemberLauncher interface {
	Launch(ctx context.Context, spec MemberSpec) (MemberHandle, error)
}

// RunFunc executes a member to completion. Both production launchers and tests
// supply one; FuncLauncher adapts it to the MemberLauncher interface by running
// it in a goroutine.
type RunFunc func(ctx context.Context, spec MemberSpec) (MemberResult, error)

// FuncLauncher is the single MemberLauncher implementation: it runs a RunFunc in
// a goroutine and exposes a handle. Production builds one whose RunFunc calls
// specialist.Executor.Run (or submits to daemon.Pool); tests build one whose
// RunFunc is a stub.
type FuncLauncher struct {
	Run RunFunc
}

// Launch starts the RunFunc for spec and returns a handle to await it.
func (l FuncLauncher) Launch(ctx context.Context, spec MemberSpec) (MemberHandle, error) {
	if l.Run == nil {
		return nil, errors.New("swarm: FuncLauncher requires a Run func")
	}
	h := &funcHandle{id: spec.ID, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		// A panic in a member's Run must fail only that member, never crash the
		// orchestrator process. Recover and surface it as the member's error.
		defer func() {
			if r := recover(); r != nil {
				h.res = MemberResult{}
				h.err = fmt.Errorf("swarm: member %s panicked: %v", spec.ID, r)
			}
		}()
		h.res, h.err = l.Run(ctx, spec)
	}()
	return h, nil
}

type funcHandle struct {
	id   string
	done chan struct{}
	res  MemberResult
	err  error
}

func (h *funcHandle) ID() string { return h.id }

// Wait blocks until the member exits and returns its result/error.
func (h *funcHandle) Wait() (MemberResult, error) {
	<-h.done
	return h.res, h.err
}
