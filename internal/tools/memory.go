package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/memory"
)

// The memory tools: read freely, write on approval.
//
// THE ASYMMETRY IS THE DESIGN. Reading a note is reading a file the user already
// has, so it needs no more ceremony than read_file. Writing one puts text into
// the user's repo that will be read back in every future session and believed —
// a note that says "this package is safe to change freely" is load-bearing the
// moment anyone trusts it. So memory_write prompts like every other write tool,
// and nothing lands silently.
//
// PLAN TASKS GET NEITHER BY DEFAULT. planReadOnlyTools is the allow-list a plan
// task's grant is validated against, and neither of these is in it — so a task
// cannot read a note (which would let a stale note steer a fan-out) or write one
// (which would let twenty tasks race to describe the same finding). That is a
// default, not a ceiling: a considered decision to grant them is a one-line
// change in that list.

const (
	MemoryToolName      = "memory"
	MemoryWriteToolName = "memory_write"
)

type memoryTool struct {
	baseTool
	paths memory.Paths
}

// NewMemoryTool reads durable notes. Paths with no directories configured makes
// the tool report that memory is unavailable rather than silently finding none —
// "you have no notes" and "notes are switched off here" are different answers.
func NewMemoryTool(paths memory.Paths) Tool {
	return memoryTool{
		baseTool: baseTool{
			name: MemoryToolName,
			description: "Read durable notes saved in earlier sessions: project conventions, decisions, and confirmed findings. " +
				"Call with no arguments to list what exists (name, scope and a one-line description) and with a name to read one. " +
				"Prefer listing first: the descriptions are there so you can choose what to open instead of reading everything.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"name":  {Type: "string", Description: "The note to read. Omit to list every note instead."},
					"scope": {Type: "string", Description: `Which store to read: "project" (shared, checked in) or "local" (this machine). Omit to search both.`},
				},
				AdditionalProperties: false,
			},
			safety:       readOnlySafety("Reads saved notes."),
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: true},
		},
		paths: paths,
	}
}

func (tool memoryTool) Run(_ context.Context, args map[string]any) Result {
	name, err := aliasedStringArg(args, []string{"name", "note", "key"}, "", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for memory: " + err.Error())
	}
	scope, err := aliasedStringArg(args, []string{"scope"}, "", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for memory: " + err.Error())
	}
	if tool.paths.ProjectDir == "" && tool.paths.LocalDir == "" {
		return errorResult("Error: memory is not available in this run.")
	}

	if strings.TrimSpace(name) == "" {
		return okResult(renderMemoryList(memory.List(tool.paths)))
	}
	for _, candidate := range memoryScopes(scope) {
		note, err := memory.Read(tool.paths, candidate, name)
		if err != nil {
			continue
		}
		return okResult(fmt.Sprintf("memory %q (%s)\n\n%s", note.Name, note.Scope, note.Body))
	}
	return errorResult(fmt.Sprintf("Error: no memory named %q. Call memory with no arguments to see what exists.", name))
}

type memoryWriteTool struct {
	baseTool
	paths memory.Paths
}

// NewMemoryWriteTool saves a durable note.
func NewMemoryWriteTool(paths memory.Paths) Tool {
	return memoryWriteTool{
		baseTool: baseTool{
			name: MemoryWriteToolName,
			description: "Save a durable note for future sessions, or delete one. " +
				"Write what a later session could not work out for itself — a convention, a decision and its reason, a finding already confirmed. " +
				"Do NOT write what the code, the tests or git history already say; a note that repeats them is one more thing to keep true. " +
				`Use scope "local" by default; "project" is checked in and shared with everyone who clones the repo, so save there only what the whole team should read.`,
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"name":        {Type: "string", Description: "Short identifier: letters, digits, hyphen and underscore."},
					"content":     {Type: "string", Description: "The note itself. Omit to DELETE the note of this name."},
					"description": {Type: "string", Description: "One line saying what this note is for. Shown when listing, so a reader can choose without opening it."},
					"scope":       {Type: "string", Description: `"local" (this machine, the default) or "project" (checked in, shared).`},
				},
				Required:             []string{"name"},
				AdditionalProperties: false,
			},
			safety:       promptSafety(SideEffectWrite, "Saves a note that future sessions will read and believe."),
			capabilities: ToolCapabilities{Effect: EffectWorkspaceWrite, ThreadSafe: false},
		},
		paths: paths,
	}
}

func (tool memoryWriteTool) Run(_ context.Context, args map[string]any) Result {
	name, err := aliasedStringArg(args, []string{"name", "note", "key"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for memory_write: " + err.Error())
	}
	content, err := aliasedStringArg(args, []string{"content", "body", "text"}, "", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for memory_write: " + err.Error())
	}
	description, err := aliasedStringArg(args, []string{"description", "summary"}, "", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for memory_write: " + err.Error())
	}
	rawScope, err := aliasedStringArg(args, []string{"scope"}, "", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for memory_write: " + err.Error())
	}
	// LOCAL BY DEFAULT. A note the model chose to keep should not land in a
	// shared, checked-in file unless someone said so — the same reasoning that
	// makes project config unable to raise a spend ceiling.
	scope := memory.ScopeLocal
	if trimmed := strings.TrimSpace(strings.ToLower(rawScope)); trimmed != "" {
		scope = memory.Scope(trimmed)
	}

	if strings.TrimSpace(content) == "" {
		if err := memory.Forget(tool.paths, scope, name); err != nil {
			return errorResult("Error: " + err.Error())
		}
		return okResult(fmt.Sprintf("Forgot %q (%s).", name, scope))
	}
	if _, err := memory.Write(tool.paths, scope, name, description, content); err != nil {
		return errorResult("Error: " + err.Error())
	}
	return okResult(fmt.Sprintf("Saved %q (%s).", name, scope))
}

func memoryScopes(requested string) []memory.Scope {
	switch memory.Scope(strings.TrimSpace(strings.ToLower(requested))) {
	case memory.ScopeProject:
		return []memory.Scope{memory.ScopeProject}
	case memory.ScopeLocal:
		return []memory.Scope{memory.ScopeLocal}
	default:
		return []memory.Scope{memory.ScopeProject, memory.ScopeLocal}
	}
}

func renderMemoryList(notes []memory.Note) string {
	if len(notes) == 0 {
		return "No saved notes yet. Use memory_write to keep something a future session could not work out for itself."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d saved note(s):\n", len(notes))
	for _, note := range notes {
		fmt.Fprintf(&b, "- %s (%s)", note.Name, note.Scope)
		if note.Description != "" {
			b.WriteString(" — ")
			b.WriteString(note.Description)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
