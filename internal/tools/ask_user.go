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
	Question           string
	Header             string
	Options            []string
	OptionDescriptions []string
	Recommended        string
	MultiSelect        bool
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
				"When the answer is likely one of a small set, you MAY include 2-4 suggested `options` and mark one as `recommended` " +
				"(it must match one of the options) — an interactive front-end shows these as a quick picker with a \"type my own\" fallback. " +
				"Options are optional: omit them for open-ended questions. " +
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
						MinItems:    intPtr(1),
						Items: &PropertySchema{
							Type: "object",
							Properties: map[string]PropertySchema{
								"question": {Type: "string", Description: "The non-empty question to ask the user.", MinLength: intPtr(1)},
								"header": {
									Type:        "string",
									Description: "Optional short title (2-3 words) used as this question's tab label in a multi-question prompt.",
								},
								"options": {
									Type:        "array",
									Description: "Optional list of 2-4 suggested answer choices (string labels) for a quick picker.",
									Items:       &PropertySchema{Type: "string"},
								},
								"optionDescriptions": {
									Type:        "array",
									Description: "Optional one-line descriptions aligned by index to options.",
									Items:       &PropertySchema{Type: "string"},
								},
								"recommended": {
									Type:        "string",
									Description: "Optional recommended choice; must match one of options. Preselected as the default in the picker.",
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
		// multiSelect is a UI hint; treat an uncoercible value as the default rather
		// than failing the whole call (mirrors the best-effort options path).
		multiSelect, _ := boolArg(object, "multiSelect", false)
		options, optionDescriptions := coerceAskUserOptions(object["options"], object["optionDescriptions"]) // best-effort; never errors
		questions = append(questions, AskUserQuestion{
			Question:           text,
			Header:             firstStringKey(object, "header"),
			Options:            options,
			OptionDescriptions: optionDescriptions,
			Recommended:        recommendedOption(object["recommended"], options), // only kept if it matches an option
			MultiSelect:        multiSelect,
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

// recommendedOption returns the model's recommended choice ONLY when it resolves to
// one of the supplied options (exact, then case-insensitive trim match). It returns
// the canonical option text (not the model's raw spelling) so the front-end can
// match it by equality, and "" when there is no usable recommendation — keeping the
// invariant that Recommended is always either empty or a member of Options.
func recommendedOption(value any, options []string) string {
	raw, ok := value.(string)
	if !ok {
		return ""
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || len(options) == 0 {
		return ""
	}
	for _, option := range options {
		if option == raw {
			return option
		}
	}
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), raw) {
			return option
		}
	}
	return ""
}

// firstStringKey returns the first non-empty string value among the given keys.
func firstStringKey(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// coerceAskUserOptions extracts option labels and their aligned descriptions from
// the raw "options" value without ever failing (options are presentation hints, so
// a malformed shape must not break the call). Each option may be a bare string (no
// description) or an object {label/value/text, description/desc/detail}. The
// question-level "optionDescriptions" array (descsValue) fills any description left
// empty, aligned by index. The returned descriptions slice is nil when every entry
// is empty, so a plain options list stays single-line.
func coerceAskUserOptions(optionsValue, descsValue any) (labels []string, descriptions []string) {
	if items, ok := optionsValue.([]any); ok {
		for _, item := range items {
			switch value := item.(type) {
			case string:
				if s := strings.TrimSpace(value); s != "" {
					labels = append(labels, s)
					descriptions = append(descriptions, "")
				}
			case map[string]any:
				label := firstStringKey(value, "label", "value", "text", "name", "title", "option")
				if label == "" {
					continue
				}
				labels = append(labels, label)
				descriptions = append(descriptions, firstStringKey(value, "description", "desc", "detail", "hint", "subtitle"))
			}
		}
	} else {
		// Non-array shape (e.g. newline-delimited string): reuse the shared coercion.
		labels = coerceStringSlice(optionsValue)
		descriptions = make([]string, len(labels))
	}
	// Fill any still-empty descriptions from the parallel optionDescriptions array.
	parallel := coerceStringSlice(descsValue)
	for index := range labels {
		if index < len(descriptions) && strings.TrimSpace(descriptions[index]) != "" {
			continue
		}
		if index < len(parallel) {
			for len(descriptions) <= index {
				descriptions = append(descriptions, "")
			}
			descriptions[index] = parallel[index]
		}
	}
	for len(descriptions) < len(labels) {
		descriptions = append(descriptions, "")
	}
	for _, description := range descriptions {
		if strings.TrimSpace(description) != "" {
			return labels, descriptions
		}
	}
	return labels, nil
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
