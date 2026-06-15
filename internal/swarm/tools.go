package swarm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/tools"
)

// Tool names. All swarm tools are additive and only act when invoked; the
// existing single "Task" tool is unchanged.
const (
	SpawnToolName   = "swarm_spawn"
	SendToolName    = "swarm_send"
	InboxToolName   = "swarm_inbox"
	StatusToolName  = "swarm_status"
	HandoffToolName = "swarm_handoff"
	CollectToolName = "swarm_collect"
)

// RegisterTools registers the six swarm tools backed by one shared Swarm. The
// caller owns the Swarm's lifetime (call Swarm.Close on shutdown).
func RegisterTools(registry *tools.Registry, sw *Swarm) {
	registry.Register(&spawnTool{sw: sw})
	registry.Register(&sendTool{sw: sw})
	registry.Register(&inboxTool{sw: sw})
	registry.Register(&statusTool{sw: sw})
	registry.Register(&handoffTool{sw: sw})
	registry.Register(&collectTool{sw: sw})
	registry.Register(&scheduleTool{sw: sw})
}

// policyFrom derives the member-inheritance policy from the live tool options so
// each spawned member runs on the orchestrator's current model + permission mode.
func policyFrom(options tools.RunOptions) Policy {
	return Policy{Model: options.Model, PermissionMode: options.PermissionMode}
}

func swarmStr(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func okResult(output, kind, summary string) tools.Result {
	return tools.Result{Status: tools.StatusOK, Output: output, Display: tools.Display{Kind: kind, Summary: summary}}
}

func errResult(format string, a ...any) tools.Result {
	return tools.Result{Status: tools.StatusError, Output: "Error: " + fmt.Sprintf(format, a...)}
}

// ---- swarm_spawn -----------------------------------------------------------

type spawnTool struct{ sw *Swarm }

func (t *spawnTool) Name() string { return SpawnToolName }
func (t *spawnTool) Description() string {
	return "Spawn a swarm member of the given agent type to run a task concurrently under a team. Returns a task id immediately; results are reported through swarm_status/swarm_collect."
}
func (t *spawnTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"agent_type": {Type: "string", Description: "Roster agent type to spawn (e.g. teammate, subagent)."},
			"task":       {Type: "string", Description: "The focused task/briefing for the member."},
			"team":       {Type: "string", Description: "Team to spawn into. Defaults to \"default\"."},
		},
		Required:             []string{"agent_type", "task"},
		AdditionalProperties: false,
	}
}
func (t *spawnTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect:      tools.SideEffectShell,
		Permission:      tools.PermissionPrompt,
		Reason:          "Spawns a swarm member sub-agent under the orchestrator's sandbox and policy.",
		AdvertiseInAuto: true,
	}
}
func (t *spawnTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return t.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (t *spawnTool) RunWithOptions(_ context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	agentType := swarmStr(args, "agent_type")
	task := swarmStr(args, "task")
	team := swarmStr(args, "team")
	if agentType == "" {
		return errResult("swarm_spawn requires agent_type")
	}
	if task == "" {
		return errResult("swarm_spawn requires task")
	}
	id, err := t.sw.Spawn(policyFrom(options), team, agentType, task, options.Cwd)
	if err != nil {
		if errors.Is(err, ErrUnknownAgentType) {
			return errResult("%v; available agent types: %s", err, strings.Join(t.sw.Registry().AgentTypes(), ", "))
		}
		return errResult("%v", err)
	}
	out := fmt.Sprintf("Spawned %s as task %s on team %s.", agentType, id, displayTeam(team))
	res := okResult(out, "swarm", out)
	res.Meta = map[string]string{"task_id": id, "team": sanitizeName(team), "agent_type": agentType}
	return res
}

// ---- swarm_send ------------------------------------------------------------

type sendTool struct{ sw *Swarm }

func (t *sendTool) Name() string { return SendToolName }
func (t *sendTool) Description() string {
	return "Send a message to another swarm member's inbox on a team."
}
func (t *sendTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"to":      {Type: "string", Description: "Recipient member/agent id."},
			"body":    {Type: "string", Description: "Message body."},
			"subject": {Type: "string", Description: "Optional subject."},
			"from":    {Type: "string", Description: "Optional sender id (the orchestrator by default)."},
			"team":    {Type: "string", Description: "Team to send within. Defaults to \"default\"."},
		},
		Required:             []string{"to", "body"},
		AdditionalProperties: false,
	}
}
func (t *sendTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect:      tools.SideEffectWrite,
		Permission:      tools.PermissionAllow,
		Reason:          "Writes a message to an owner-only swarm inbox file.",
		AdvertiseInAuto: true,
	}
}
func (t *sendTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return t.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (t *sendTool) RunWithOptions(_ context.Context, args map[string]any, _ tools.RunOptions) tools.Result {
	to := swarmStr(args, "to")
	body := swarmStr(args, "body")
	if to == "" {
		return errResult("swarm_send requires to")
	}
	if body == "" {
		return errResult("swarm_send requires body")
	}
	team := swarmStr(args, "team")
	from := swarmStr(args, "from")
	if from == "" {
		from = "orchestrator"
	}
	msg := Message{From: from, Subject: swarmStr(args, "subject"), Body: body}
	if err := t.sw.Mailbox().Send(team, to, msg); err != nil {
		return errResult("%v", err)
	}
	out := fmt.Sprintf("Delivered message to %s on team %s.", sanitizeName(to), displayTeam(team))
	return okResult(out, "swarm", out)
}

// ---- swarm_inbox -----------------------------------------------------------

type inboxTool struct{ sw *Swarm }

func (t *inboxTool) Name() string { return InboxToolName }
func (t *inboxTool) Description() string {
	return "Read a member's inbox on a team. Returns all messages and marks previously-unread ones as read."
}
func (t *inboxTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"agent": {Type: "string", Description: "Member/agent id whose inbox to read."},
			"team":  {Type: "string", Description: "Team the inbox belongs to. Defaults to \"default\"."},
		},
		Required:             []string{"agent"},
		AdditionalProperties: false,
	}
}
func (t *inboxTool) Safety() tools.Safety {
	return tools.Safety{
		// Reading an inbox marks messages read (a benign metadata flip in an
		// owner-only file), so it is classified as a write but auto-allowed.
		SideEffect:      tools.SideEffectWrite,
		Permission:      tools.PermissionAllow,
		Reason:          "Reads and marks-read an owner-only swarm inbox file.",
		AdvertiseInAuto: true,
	}
}
func (t *inboxTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return t.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (t *inboxTool) RunWithOptions(_ context.Context, args map[string]any, _ tools.RunOptions) tools.Result {
	agent := swarmStr(args, "agent")
	if agent == "" {
		return errResult("swarm_inbox requires agent")
	}
	team := swarmStr(args, "team")
	messages, err := t.sw.Mailbox().ReadAndConsume(team, agent)
	if err != nil {
		return errResult("%v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Inbox for %s on team %s: %d message(s)\n", sanitizeName(agent), displayTeam(team), len(messages))
	for i, msg := range messages {
		state := "read"
		if !msg.Read {
			state = "unread"
		}
		subject := msg.Subject
		if subject != "" {
			subject = " [" + subject + "]"
		}
		fmt.Fprintf(&b, "  [%d] (%s) from %s%s: %s\n", i, state, msg.From, subject, msg.Body)
	}
	out := strings.TrimRight(b.String(), "\n")
	return okResult(out, "swarm", fmt.Sprintf("%d message(s)", len(messages)))
}

// ---- swarm_status ----------------------------------------------------------

type statusTool struct{ sw *Swarm }

func (t *statusTool) Name() string { return StatusToolName }
func (t *statusTool) Description() string {
	return "Report swarm task status: counts by state and per-task lines. Optionally scoped to one team."
}
func (t *statusTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"team": {Type: "string", Description: "Optional team to scope to."},
		},
		AdditionalProperties: false,
	}
}
func (t *statusTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect:      tools.SideEffectRead,
		Permission:      tools.PermissionAllow,
		Reason:          "Reports in-memory swarm task status.",
		AdvertiseInAuto: true,
	}
}
func (t *statusTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return t.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (t *statusTool) RunWithOptions(_ context.Context, args map[string]any, _ tools.RunOptions) tools.Result {
	team := swarmStr(args, "team")
	tasks := t.sw.Coordinator().List()
	if team != "" {
		want := sanitizeName(team)
		filtered := tasks[:0:0]
		for _, task := range tasks {
			if task.Team == want {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}
	return okResult(renderTasks(t.sw.Coordinator(), tasks, team), "swarm", fmt.Sprintf("%d task(s)", len(tasks)))
}

// ---- swarm_handoff ---------------------------------------------------------

type handoffTool struct{ sw *Swarm }

func (t *handoffTool) Name() string { return HandoffToolName }
func (t *handoffTool) Description() string {
	return "Hand a task off to a fresh member of another agent type, delivering an optional note to the new member's inbox."
}
func (t *handoffTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"task_id":       {Type: "string", Description: "Task id to hand off."},
			"to_agent_type": {Type: "string", Description: "Roster agent type to take over."},
			"note":          {Type: "string", Description: "Optional handoff note delivered to the new member's inbox."},
			"team":          {Type: "string", Description: "Team the task belongs to. Defaults to \"default\"."},
		},
		Required:             []string{"task_id", "to_agent_type"},
		AdditionalProperties: false,
	}
}
func (t *handoffTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect:      tools.SideEffectShell,
		Permission:      tools.PermissionPrompt,
		Reason:          "Spawns a replacement swarm member to take over a task, and writes the handoff note to the new member's inbox.",
		AdvertiseInAuto: true,
	}
}
func (t *handoffTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return t.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (t *handoffTool) RunWithOptions(_ context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	taskID := swarmStr(args, "task_id")
	to := swarmStr(args, "to_agent_type")
	if taskID == "" {
		return errResult("swarm_handoff requires task_id")
	}
	if to == "" {
		return errResult("swarm_handoff requires to_agent_type")
	}
	team := swarmStr(args, "team")
	newID, err := t.sw.Handoff(policyFrom(options), team, taskID, to, swarmStr(args, "note"))
	if err != nil {
		return errResult("%v", err)
	}
	out := fmt.Sprintf("Handed off task %s to %s as task %s on team %s.", taskID, to, newID, displayTeam(team))
	res := okResult(out, "swarm", out)
	res.Meta = map[string]string{"task_id": newID, "previous_task_id": taskID}
	return res
}

// ---- swarm_collect ---------------------------------------------------------

type collectTool struct{ sw *Swarm }

func (t *collectTool) Name() string { return CollectToolName }
func (t *collectTool) Description() string {
	return "Collect the results of a team's tasks (status, result, and any error per task)."
}
func (t *collectTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"team": {Type: "string", Description: "Team to collect from. Defaults to \"default\"."},
		},
		AdditionalProperties: false,
	}
}
func (t *collectTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect:      tools.SideEffectRead,
		Permission:      tools.PermissionAllow,
		Reason:          "Reports in-memory swarm task results.",
		AdvertiseInAuto: true,
	}
}
func (t *collectTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return t.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (t *collectTool) RunWithOptions(_ context.Context, args map[string]any, _ tools.RunOptions) tools.Result {
	team := swarmStr(args, "team")
	tasks := t.sw.Collect(team)
	var b strings.Builder
	fmt.Fprintf(&b, "Results for team %s: %d task(s)\n", displayTeam(team), len(tasks))
	for _, task := range tasks {
		line := fmt.Sprintf("  - %s [%s] %s", task.ID, task.Status, task.Description)
		if task.Result != "" {
			line += "\n      result: " + collapse(task.Result)
		}
		if task.Err != "" {
			line += "\n      error: " + collapse(task.Err)
		}
		b.WriteString(line + "\n")
	}
	out := strings.TrimRight(b.String(), "\n")
	return okResult(out, "swarm", fmt.Sprintf("%d task(s)", len(tasks)))
}

// renderTasks formats a status summary + per-task lines, colored per agent.
func renderTasks(coord *Coordinator, tasks []Task, team string) string {
	s := coord.Summarize()
	var b strings.Builder
	scope := "all teams"
	if strings.TrimSpace(team) != "" {
		scope = "team " + sanitizeName(team)
	}
	fmt.Fprintf(&b, "Swarm status (%s): %d task(s) — %d running, %d pending, %d done, %d failed, %d handed-off\n",
		scope, len(tasks), s.Running, s.Pending, s.Done, s.Failed, s.HandedOff)
	sorted := make([]Task, len(tasks))
	copy(sorted, tasks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, task := range sorted {
		fmt.Fprintf(&b, "  - %s [%s] (%s) %s\n", task.ID, task.Status, coord.Color(task.AgentID), collapse(task.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

// collapse trims a possibly-multiline string to a single compact line for
// status/collect output.
func collapse(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	const max = 200 // runes, not bytes — never slice mid-character
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}
