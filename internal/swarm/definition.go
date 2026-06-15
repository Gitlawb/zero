// Package swarm adds a multi-agent SWARM on top of zero's single sub-agent
// mechanism (internal/specialist): an orchestrator can spawn and coordinate
// MULTIPLE specialist members that run concurrently, communicate via per-agent
// mailboxes, and hand work off. It composes internal/specialist (to launch each
// member), internal/daemon (pooled workers when a daemon is running) and
// internal/background. Everything is additive and opt-in — the existing single
// "task" tool is unchanged; swarm tools are only active when invoked.
//
// Every member runs under the SAME sandbox + risk/autonomy policy as the
// orchestrator: the swarm never grants a member more authority than its parent.
package swarm

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// ErrUnknownAgentType is returned when a definition lookup misses.
var ErrUnknownAgentType = errors.New("swarm: unknown agent type")

// modelInherit is the sentinel meaning "use the orchestrator's model" — members
// never silently pick a different (e.g. more capable) model.
const modelInherit = "inherit"

// Definition is one entry in the agent roster: how a member of a given type
// behaves (agent type, when to use it, model — "inherit" by default —,
// permission mode, and a system-prompt builder).
type Definition struct {
	AgentType      string
	WhenToUse      string
	Model          string // "inherit" => use the orchestrator's model
	PermissionMode string
	// SystemPrompt returns the member's system prompt for the given task context.
	// It is a func so a definition can fold the task briefing in.
	SystemPrompt func(ctx PromptContext) string
}

// PromptContext is the minimal context handed to a definition's SystemPrompt.
type PromptContext struct {
	Team string
	Task string
}

// Registry is the agent roster: definitions looked up by agent type. Built-ins
// are seeded at construction; user-defined definitions extend or override them.
type Registry struct {
	mu   sync.RWMutex
	defs map[string]Definition
}

// NewRegistry builds a registry seeded with the built-in roster.
func NewRegistry() *Registry {
	r := &Registry{defs: map[string]Definition{}}
	for _, def := range builtinDefinitions() {
		r.defs[def.AgentType] = def
	}
	return r
}

// Register adds or overrides a definition (user-defined agents extend the
// built-ins). An empty AgentType is rejected.
func (r *Registry) Register(def Definition) error {
	agentType := strings.TrimSpace(def.AgentType)
	if agentType == "" {
		return errors.New("swarm: definition requires an agentType")
	}
	if def.Model == "" {
		def.Model = modelInherit
	}
	def.AgentType = agentType
	r.mu.Lock()
	r.defs[agentType] = def
	r.mu.Unlock()
	return nil
}

// Lookup returns the definition for agentType, or ErrUnknownAgentType.
func (r *Registry) Lookup(agentType string) (Definition, error) {
	r.mu.RLock()
	def, ok := r.defs[strings.TrimSpace(agentType)]
	r.mu.RUnlock()
	if !ok {
		return Definition{}, ErrUnknownAgentType
	}
	return def, nil
}

// AgentTypes returns the registered agent types, sorted, for status/help.
func (r *Registry) AgentTypes() []string {
	r.mu.RLock()
	types := make([]string, 0, len(r.defs))
	for t := range r.defs {
		types = append(types, t)
	}
	r.mu.RUnlock()
	sort.Strings(types)
	return types
}

// builtinDefinitions returns the seeded roster (teammate, subagent) — kept
// minimal; user-defined agents extend it via Register.
func builtinDefinitions() []Definition {
	return []Definition{
		{
			AgentType: "teammate",
			WhenToUse: "In-process teammate for parallel task execution; delegate work to run alongside the orchestrator.",
			Model:     modelInherit,
			// Empty PermissionMode => inherit the orchestrator's mode (never widened).
			SystemPrompt: func(ctx PromptContext) string {
				return "You are a teammate agent collaborating with an orchestrator on team " +
					displayTeam(ctx.Team) + ". Complete your assigned task and report results.\n\nTask: " + ctx.Task
			},
		},
		{
			AgentType: "subagent",
			WhenToUse: "General-purpose subagent for an isolated, delegated task; starts with zero prior context.",
			Model:     modelInherit,
			SystemPrompt: func(ctx PromptContext) string {
				return "You are a subagent spawned to complete a specific task. You start with zero context — " +
					"the briefing below is all you know.\n\nTask: " + ctx.Task
			},
		},
	}
}

func displayTeam(team string) string {
	if strings.TrimSpace(team) == "" {
		return "default"
	}
	return team
}
