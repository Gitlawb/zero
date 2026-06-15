package swarm

import (
	"context"

	"github.com/Gitlawb/zero/internal/specialist"
)

// NewSpecialistLauncher adapts internal/specialist.Executor into a
// MemberLauncher: each swarm member runs as a specialist sub-agent. This is the
// production "direct" launch path (used when no daemon pool is wired). The
// member inherits the orchestrator's model via spec.Model and runs in spec.Cwd,
// so it stays under the same sandbox + policy as its parent — the swarm never
// grants a member more authority.
//
// agentType maps to the specialist agent name; the specialist's own definition
// for that name governs the member's system prompt and tools. RunInBackground is
// deliberately left false: the swarm provides concurrency itself (one goroutine
// per member), so each Run executes to completion inside its goroutine.
func NewSpecialistLauncher(executor specialist.Executor) MemberLauncher {
	return FuncLauncher{Run: func(ctx context.Context, spec MemberSpec) (MemberResult, error) {
		res, err := executor.Run(ctx, specialist.TaskParameters{
			Name:        spec.AgentType,
			Prompt:      spec.Task,
			Description: spec.AgentType + " · team " + spec.Team,
		}, specialist.TaskRunOptions{
			ParentModel: spec.Model,
			Cwd:         spec.Cwd,
		})
		if err != nil {
			return MemberResult{}, err
		}
		return MemberResult{Result: res.Result.Output, SessionID: res.SessionID}, nil
	}}
}
