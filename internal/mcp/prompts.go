package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// jsonRPCPromptNotFound is the MCP convention for an unknown prompt name. It is
// distinct from the transport-level method-not-found code.
const jsonRPCPromptNotFound = -32002

// Prompt describes a curated prompt template advertised through prompts/list.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes one templated argument of a prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptMessage is one message in a rendered prompt (prompts/get result).
type PromptMessage struct {
	Role    string               `json:"role"`
	Content PromptMessageContent `json:"content"`
}

// PromptMessageContent carries the rendered text of a prompt message.
type PromptMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// promptTemplate is the internal definition of a curated prompt: its metadata
// plus the message templates that {{arg}} substitution is applied to.
type promptTemplate struct {
	prompt   Prompt
	messages []promptMessageTemplate
}

type promptMessageTemplate struct {
	role string
	text string
}

// curatedPrompts is ZERO's published prompt catalogue. These mirror ZERO's own
// slash-command / spec-mode workflows (code review, spec drafting, workspace
// explanation) as reusable templates other agents can pull; internal system
// prompts are deliberately not exposed.
var curatedPrompts = []promptTemplate{
	{
		prompt: Prompt{
			Name:        "code_review",
			Description: "Review a code change for correctness, clarity, and risk.",
			Arguments: []PromptArgument{
				{Name: "diff", Description: "The unified diff or code to review.", Required: true},
				{Name: "focus", Description: "Optional area to focus the review on (e.g. security, performance).", Required: false},
			},
		},
		messages: []promptMessageTemplate{
			{
				role: "user",
				text: "Review the following change. Call out correctness bugs first, then " +
					"clarity and maintainability issues, then anything risky. Be specific " +
					"and cite the exact lines.\n\nExtra focus: {{focus}}\n\n```\n{{diff}}\n```",
			},
		},
	},
	{
		prompt: Prompt{
			Name:        "draft_spec",
			Description: "Draft a concrete implementation spec for a requested change.",
			Arguments: []PromptArgument{
				{Name: "task", Description: "What needs to be built or changed.", Required: true},
				{Name: "context", Description: "Optional relevant files, constraints, or background.", Required: false},
			},
		},
		messages: []promptMessageTemplate{
			{
				role: "user",
				text: "Draft an implementation spec for the task below. Choose one concrete " +
					"approach (no unresolved Option A/Option B). Include: Goal, Relevant " +
					"files/components, Implementation steps, Tests and verification, Risks " +
					"and edge cases, and Out of scope.\n\nTask: {{task}}\n\nContext: {{context}}",
			},
		},
	},
	{
		prompt: Prompt{
			Name:        "explain_code",
			Description: "Explain what a piece of code does and how it fits together.",
			Arguments: []PromptArgument{
				{Name: "code", Description: "The code to explain.", Required: true},
			},
		},
		messages: []promptMessageTemplate{
			{
				role: "user",
				text: "Explain the following code: what it does, the key control flow, and " +
					"any notable edge cases or assumptions. Keep it concise.\n\n```\n{{code}}\n```",
			},
		},
	},
}

// curatedPrompt looks up a prompt template by name.
func curatedPrompt(name string) (promptTemplate, bool) {
	for _, template := range curatedPrompts {
		if template.prompt.Name == name {
			return template, true
		}
	}
	return promptTemplate{}, false
}

// listPrompts returns the curated prompt catalogue sorted by name for stable
// output.
func listPrompts() []Prompt {
	prompts := make([]Prompt, 0, len(curatedPrompts))
	for _, template := range curatedPrompts {
		prompts = append(prompts, template.prompt)
	}
	sort.Slice(prompts, func(left int, right int) bool {
		return prompts[left].Name < prompts[right].Name
	})
	return prompts
}

// getPromptResult is the rendered prompts/get payload.
type getPromptResult struct {
	Description string          `json:"description"`
	Messages    []PromptMessage `json:"messages"`
}

// getPrompt renders a curated prompt with {{arg}} substitution. It returns a
// JSON-RPC error code alongside the error so the caller can map missing-name
// (invalid params) and unknown-prompt (not found) distinctly.
func getPrompt(rawParams json.RawMessage) (getPromptResult, int, error) {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if len(rawParams) > 0 {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return getPromptResult{}, jsonRPCInvalidParams, fmt.Errorf("invalid prompts/get params: %w", err)
		}
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return getPromptResult{}, jsonRPCInvalidParams, errors.New("prompts/get requires a prompt name")
	}
	template, ok := curatedPrompt(name)
	if !ok {
		return getPromptResult{}, jsonRPCPromptNotFound, fmt.Errorf("unknown prompt: %s", name)
	}
	// Honor the contract advertised by prompts/list: a required argument must be
	// supplied and non-empty, otherwise the call is invalid rather than silently
	// rendering a blank.
	for _, arg := range template.prompt.Arguments {
		if !arg.Required {
			continue
		}
		if value, present := params.Arguments[arg.Name]; !present || strings.TrimSpace(value) == "" {
			return getPromptResult{}, jsonRPCInvalidParams, fmt.Errorf("prompts/get: missing required argument %q", arg.Name)
		}
	}

	messages := make([]PromptMessage, 0, len(template.messages))
	for _, message := range template.messages {
		messages = append(messages, PromptMessage{
			Role: message.role,
			Content: PromptMessageContent{
				Type: "text",
				Text: substituteArgs(message.text, params.Arguments),
			},
		})
	}
	return getPromptResult{
		Description: template.prompt.Description,
		Messages:    messages,
	}, 0, nil
}

// substituteArgs renders a TEMPLATE by replacing each {{name}} placeholder with
// the matching argument value (or blank when not supplied). It scans the
// template left-to-right and writes argument values verbatim, so {{...}} that
// appears inside a supplied value (Helm/Mustache/Actions snippets in a pasted
// diff, etc.) is preserved and never re-interpreted as a placeholder.
func substituteArgs(text string, arguments map[string]string) string {
	var b strings.Builder
	for {
		open := strings.Index(text, "{{")
		if open < 0 {
			b.WriteString(text)
			break
		}
		rel := strings.Index(text[open:], "}}")
		if rel < 0 {
			b.WriteString(text)
			break
		}
		b.WriteString(text[:open])
		name := strings.TrimSpace(text[open+2 : open+rel])
		b.WriteString(arguments[name]) // missing arg -> "" (blank)
		text = text[open+rel+2:]
	}
	return b.String()
}
