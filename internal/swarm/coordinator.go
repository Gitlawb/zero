package swarm

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// TaskStatus is the lifecycle state of a swarm task.
type TaskStatus string

const (
	// StatusPending: registered but the member has not started.
	StatusPending TaskStatus = "pending"
	// StatusRunning: the member is executing.
	StatusRunning TaskStatus = "running"
	// StatusDone: the member finished successfully.
	StatusDone TaskStatus = "done"
	// StatusFailed: the member exited with an error.
	StatusFailed TaskStatus = "failed"
	// StatusHandedOff: ownership was transferred to another agent.
	StatusHandedOff TaskStatus = "handed-off"
)

// terminal reports whether a status is final (no further transitions expected).
func (s TaskStatus) terminal() bool {
	return s == StatusDone || s == StatusFailed || s == StatusHandedOff
}

// valid reports whether a status is one of the known lifecycle states. Unknown
// statuses are rejected (fail closed) so a task can never enter an undefined
// state that Summarize and the lifecycle logic cannot reason about.
func (s TaskStatus) valid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusDone, StatusFailed, StatusHandedOff:
		return true
	default:
		return false
	}
}

// Task is one unit of work tracked by the coordinator. It is returned by value
// from snapshots so callers cannot mutate coordinator state without the lock.
type Task struct {
	ID          string
	AgentID     string
	Team        string
	Description string
	Status      TaskStatus
	Result      string
	Err         string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// agentColors palette for stable per-agent coloring in the TUI/status. Provider
// -neutral ANSI-ish names; the renderer maps them to styles.
var agentColors = []string{"cyan", "magenta", "green", "yellow", "blue", "red"}

// Coordinator is the in-memory task registry + team color assigner shared by an
// orchestrator and its members. It is safe for concurrent use.
type Coordinator struct {
	mu         sync.RWMutex
	tasks      map[string]*Task
	colors     map[string]string // agentID -> color
	colorIndex int
	now        func() time.Time // injectable clock for tests
}

// NewCoordinator returns an empty coordinator using the wall clock.
func NewCoordinator() *Coordinator {
	return &Coordinator{
		tasks:  map[string]*Task{},
		colors: map[string]string{},
		now:    time.Now,
	}
}

// ErrTaskExists is returned when registering a duplicate task ID.
var ErrTaskExists = errors.New("swarm: task already registered")

// ErrUnknownTask is returned when updating/removing a missing task ID.
var ErrUnknownTask = errors.New("swarm: unknown task")

// Register adds a new pending task. The ID must be unique and non-empty.
func (c *Coordinator) Register(id, agentID, team, description string) (Task, error) {
	if id == "" {
		return Task{}, errors.New("swarm: task id is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.tasks[id]; ok {
		return Task{}, fmt.Errorf("%w: %s", ErrTaskExists, id)
	}
	now := c.now()
	t := &Task{
		ID:          id,
		AgentID:     agentID,
		Team:        team,
		Description: description,
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	c.tasks[id] = t
	c.assignColorLocked(agentID)
	return *t, nil
}

// SetStatus transitions a task. Transitions out of a terminal state are
// rejected (fail closed) so a late member update cannot resurrect a finished
// task.
func (c *Coordinator) SetStatus(id string, status TaskStatus) error {
	if !status.valid() {
		return fmt.Errorf("swarm: invalid task status %q", status)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.tasks[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTask, id)
	}
	if t.Status.terminal() && t.Status != status {
		return fmt.Errorf("swarm: task %s is %s (terminal); cannot move to %s", id, t.Status, status)
	}
	t.Status = status
	t.UpdatedAt = c.now()
	return nil
}

// Complete marks a task done with its result.
func (c *Coordinator) Complete(id, result string) error {
	return c.finish(id, StatusDone, result, "")
}

// Fail marks a task failed with an error message.
func (c *Coordinator) Fail(id, errMsg string) error {
	return c.finish(id, StatusFailed, "", errMsg)
}

func (c *Coordinator) finish(id string, status TaskStatus, result, errMsg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.tasks[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTask, id)
	}
	if t.Status.terminal() {
		return fmt.Errorf("swarm: task %s already %s", id, t.Status)
	}
	t.Status = status
	t.Result = result
	t.Err = errMsg
	t.UpdatedAt = c.now()
	return nil
}

// Reassign transfers a task to a new agent (handoff/orphan-adoption). The task
// returns to pending under the new owner unless it is already terminal.
func (c *Coordinator) Reassign(id, newAgentID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.tasks[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTask, id)
	}
	if t.Status.terminal() {
		return fmt.Errorf("swarm: task %s already %s; cannot reassign", id, t.Status)
	}
	t.AgentID = newAgentID
	t.Status = StatusPending
	t.UpdatedAt = c.now()
	c.assignColorLocked(newAgentID)
	return nil
}

// Get returns a snapshot of one task.
func (c *Coordinator) Get(id string) (Task, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// List returns snapshots of all tasks, ordered by creation time then ID for
// stable output.
func (c *Coordinator) List() []Task {
	c.mu.RLock()
	out := make([]Task, 0, len(c.tasks))
	for _, t := range c.tasks {
		out = append(out, *t)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Color returns the stable color assigned to an agent (assigning one on first
// use), so status output colors each member consistently.
func (c *Coordinator) Color(agentID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.assignColorLocked(agentID)
}

func (c *Coordinator) assignColorLocked(agentID string) string {
	if agentID == "" {
		return ""
	}
	if color, ok := c.colors[agentID]; ok {
		return color
	}
	color := agentColors[c.colorIndex%len(agentColors)]
	c.colors[agentID] = color
	c.colorIndex++
	return color
}

// Summary is an aggregate count of tasks by status for the status tool.
type Summary struct {
	Total     int
	Pending   int
	Running   int
	Done      int
	Failed    int
	HandedOff int
}

// Summarize aggregates current task counts.
func (c *Coordinator) Summarize() Summary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := Summary{Total: len(c.tasks)}
	for _, t := range c.tasks {
		switch t.Status {
		case StatusPending:
			s.Pending++
		case StatusRunning:
			s.Running++
		case StatusDone:
			s.Done++
		case StatusFailed:
			s.Failed++
		case StatusHandedOff:
			s.HandedOff++
		}
	}
	return s
}
