package tools

import (
	"context"
	"fmt"
	"strings"
)

// AskUserQuestion is one clarifying question the agent wants the user to answer.
// Options/MultiSelect are presentation hints for an interactive front-end; the
// tool itself never blocks on input.
type AskUserQuestion struct {
	Question    string
	Options     []string
	MultiSelect bool
}

// askUserNonInteractiveMessage is returned both by the tool's own Run() fallback
// and by the agent loop when no interactive user is wired up, so the model gets
// identical, actionable guidance in either path.
const askUserNonInteractiveMessage = "No interactive user is available to answer questions. " +
	"Proceed with your best assumption, explicitly stating the assumptions you are making."

type askUserTool struct {
	baseTool
}

// NewAskUserTool builds the ask_user tool. It is read-only (it gathers input,
// never mutates the workspace). The agent loop intercepts ask_user calls and
// routes them to an interactive front-end when one exists; this tool's Run() is
// the fallback used when nothing intercepts the call (e.g. headless runs).
func NewAskUserTool() *askUserTool {
	return &askUserTool{
		baseTool: baseTool{
			name: "ask_user",
			description: "Ask the user one or more clarifying questions and wait for their answers. " +
				"Use ONLY for genuinely blocking ambiguity that you cannot resolve from the workspace or reasonable assumptions. " +
				"If no interactive user is available, this returns guidance to proceed with your best assumption instead of blocking.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"header": {
						Type:        "string",
						Description: "Optional short heading shown above the questions.",
					},
					"questions": {
						Type:        "array",
						Description: "One or more questions to ask the user.",
						Items: &PropertySchema{
							Type: "object",
							Properties: map[string]PropertySchema{
								"question": {Type: "string", Description: "The question to ask the user."},
								"options": {
									Type:        "array",
									Description: "Optional list of suggested answer choices.",
									Items:       &PropertySchema{Type: "string"},
								},
								"multiSelect": {
									Type:        "boolean",
									Description: "Whether multiple options may be selected (defaults to false).",
								},
							},
							Required: []string{"question"},
						},
					},
				},
				Required:             []string{"questions"},
				AdditionalProperties: false,
			},
			safety: readOnlySafety("Asks the user clarifying questions; gathers input only."),
		},
	}
}

// Run is the fallback path: it is only reached when nothing intercepted the call
// (no interactive user). It validates the arguments so a malformed call still
// gets useful feedback, then tells the model to proceed with its best assumption.
// It never blocks on input.
func (tool *askUserTool) Run(_ context.Context, args map[string]any) Result {
	if _, err := ParseAskUserQuestions(args); err != nil {
		return errorResult("Error: Invalid arguments for ask_user: " + err.Error())
	}
	return okResult(askUserNonInteractiveMessage)
}

// AskUserNonInteractiveMessage exposes the shared graceful-degradation message so
// the agent loop and the tool fallback stay in lock-step.
func AskUserNonInteractiveMessage() string {
	return askUserNonInteractiveMessage
}

// ParseAskUserQuestions extracts the questionnaire from raw tool arguments. It is
// shared by the tool's Run() fallback and the agent loop's interactive path so
// both validate identically.
func ParseAskUserQuestions(args map[string]any) ([]AskUserQuestion, error) {
	raw, ok := args["questions"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("questions is required")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("questions must be an array")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("questions must contain at least one question")
	}

	questions := make([]AskUserQuestion, 0, len(items))
	for index, item := range items {
		// Weak models sometimes send a question as a bare string instead of an
		// object — accept that as a free-text question.
		if text, ok := item.(string); ok {
			if strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("question %d must not be empty", index+1)
			}
			questions = append(questions, AskUserQuestion{Question: text})
			continue
		}
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("question %d must be an object or string", index+1)
		}
		text, err := questionTextArg(object)
		if err != nil {
			return nil, fmt.Errorf("question %d %s", index+1, err.Error())
		}
		multiSelect, err := boolArg(object, "multiSelect", false)
		if err != nil {
			return nil, fmt.Errorf("question %d %s", index+1, err.Error())
		}
		questions = append(questions, AskUserQuestion{
			Question:    text,
			Options:     coerceOptionStrings(object["options"]), // best-effort; never errors
			MultiSelect: multiSelect,
		})
	}
	return questions, nil
}

// questionTextArg reads the question text, accepting common key variants used by
// weaker models. It enforces a non-empty trimmed string but, unlike
// aliasedStringArg, treats a present-but-non-string or blank value as "not
// present" and falls through to the next alias (question text is best-effort
// across spellings, not type-strict).
func questionTextArg(object map[string]any) (string, error) {
	for _, key := range []string{"question", "prompt", "text", "q", "title"} {
		if v, ok := object[key]; ok && v != nil {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("question is required")
}

// coerceOptionStrings turns whatever a model put in "options" into a string slice
// without ever failing — options are presentation hints, so a malformed shape must
// not break the whole ask_user call. It delegates to the shared coerceStringSlice
// so there is a single coercion path across the package.
func coerceOptionStrings(value any) []string {
	return coerceStringSlice(value)
}

// FormatAskUserAnswers renders question/answer pairs into a clear, model-readable
// block. Missing answers are surfaced explicitly so the model never silently
// treats an unanswered question as answered.
func FormatAskUserAnswers(questions []AskUserQuestion, answers []string) string {
	lines := make([]string, 0, len(questions)*3)
	for index, question := range questions {
		answer := ""
		if index < len(answers) {
			answer = strings.TrimSpace(answers[index])
		}
		if answer == "" {
			answer = "(no answer provided)"
		}
		lines = append(lines, fmt.Sprintf("%d. [question] %s", index+1, question.Question))
		lines = append(lines, "[answer] "+answer)
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
