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
//
// spec.PermissionMode (resolved + clamped by buildSpec, never above the
// orchestrator's) is propagated so the child only runs unsafe/high autonomy when
// the orchestrator is itself unsafe; otherwise it runs non-unsafe. An empty mode
// is substituted with the non-unsafe "auto" default so a swarm member can never
// fall into the historical empty-means-high path — that is the Task tool's
// behavior, not the swarm's.
func NewSpecialistLauncher(executor specialist.Executor) MemberLauncher {
	return FuncLauncher{Run: func(ctx context.Context, spec MemberSpec) (MemberResult, error) {
		permissionMode := spec.PermissionMode
		if permissionMode == "" {
			permissionMode = permissionModeAuto
		}
		res, err := executor.Run(ctx, specialist.TaskParameters{
			Name:        spec.AgentType,
			Prompt:      spec.Task,
			Description: spec.AgentType + " · team " + spec.Team,
		}, specialist.TaskRunOptions{
			ParentModel:    spec.Model,
			Cwd:            spec.Cwd,
			PermissionMode: permissionMode,
		})
		if err != nil {
			return MemberResult{}, err
		}
		return MemberResult{Result: res.Result.Output, SessionID: res.SessionID}, nil
	}}
}
