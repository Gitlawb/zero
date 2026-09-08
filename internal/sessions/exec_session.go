package sessions

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"
)

type ExecMode string

const (
	ModeNew    ExecMode = "new"
	ModeResume ExecMode = "resume"
	ModeFork   ExecMode = "fork"
)

type ExecError struct {
	message string
}

func (err ExecError) Error() string {
	return err.message
}

type PrepareExecOptions struct {
	Store            *Store
	SessionID        string
	Title            string
	Cwd              string
	ModelID          string
	Provider         string
	Tag              string
	Depth            int
	CallingSessionID string
	CallingToolUseID string
	AgentName        string
	TaskID           string
	Resume           string
	ResumeLatest     bool
	Fork             string
}

type PreparedExec struct {
	Mode          ExecMode
	Session       Metadata
	ContextEvents []Event
	Store         *Store
}

func PrepareExec(options PrepareExecOptions) (PreparedExec, error) {
	resumeID := strings.TrimSpace(options.Resume)
	forkID := strings.TrimSpace(options.Fork)
	if (resumeID != "" || options.ResumeLatest) && forkID != "" {
		return PreparedExec{}, ExecError{"Use either --resume or --fork, not both."}
	}

	store := options.Store
	if store == nil {
		store = NewStore(StoreOptions{})
	}

	if forkID != "" {
		parent, err := store.Get(forkID)
		if err != nil {
			return PreparedExec{}, err
		}
		if parent == nil {
			return PreparedExec{}, ExecError{"Zero session not found: " + forkID}
		}
		contextEvents, err := readExecContextEvents(store, parent.SessionID)
		if err != nil {
			return PreparedExec{}, err
		}
		session, err := store.Fork(parent.SessionID, ForkInput{
			SessionID: options.SessionID,
			Title:     firstNonEmpty(options.Title, forkTitle(parent.Title)),
			Cwd:       options.Cwd,
			ModelID:   options.ModelID,
			Provider:  options.Provider,
		})
		if err != nil {
			return PreparedExec{}, err
		}
		return PreparedExec{Mode: ModeFork, Session: session, ContextEvents: contextEvents, Store: store}, nil
	}

	if resumeID != "" || options.ResumeLatest {
		sessionID := resumeID
		if sessionID == "" && options.ResumeLatest {
			latest, err := store.LatestResumable()
			if err != nil {
				return PreparedExec{}, err
			}
			if latest == nil {
				return PreparedExec{}, ExecError{"No Zero sessions available to resume."}
			}
			sessionID = latest.SessionID
		}
		session, err := store.Get(sessionID)
		if err != nil {
			return PreparedExec{}, err
		}
		if session == nil {
			return PreparedExec{}, ExecError{"Zero session not found: " + sessionID}
		}
		if !IsResumableKind(session.SessionKind) {
			return PreparedExec{}, ExecError{"Zero session is not resumable: " + sessionID}
		}
		contextEvents, err := readExecContextEvents(store, session.SessionID)
		if err != nil {
			return PreparedExec{}, err
		}
		return PreparedExec{Mode: ModeResume, Session: *session, ContextEvents: contextEvents, Store: store}, nil
	}

	createInput := CreateInput{
		SessionID: options.SessionID,
		Title:     options.Title,
		Cwd:       options.Cwd,
		ModelID:   options.ModelID,
		Provider:  options.Provider,
		Tag:       options.Tag,
		Depth:     options.Depth,
	}
	if strings.TrimSpace(options.CallingSessionID) != "" {
		parentSessionID := strings.TrimSpace(options.CallingSessionID)
		parent, err := store.Get(parentSessionID)
		if err != nil {
			return PreparedExec{}, err
		}
		if parent == nil {
			return PreparedExec{}, ExecError{"Zero parent session not found: " + parentSessionID}
		}
		createInput.SessionKind = SessionKindChild
		createInput.ParentSessionID = parent.SessionID
		createInput.RootSessionID = firstNonEmpty(parent.RootSessionID, parent.SessionID)
		createInput.AgentName = strings.TrimSpace(options.AgentName)
		createInput.TaskID = strings.TrimSpace(firstNonEmpty(options.TaskID, options.SessionID))
		createInput.SpawnedFromEventID = strings.TrimSpace(options.CallingToolUseID)
	}
	session, err := store.Create(createInput)
	if err != nil {
		return PreparedExec{}, err
	}
	return PreparedExec{Mode: ModeNew, Session: session, ContextEvents: []Event{}, Store: store}, nil
}

func readExecContextEvents(store *Store, sessionID string) ([]Event, error) {
	contextEvents, err := store.ReadRehydratedEvents(sessionID)
	if err == nil {
		return contextEvents, nil
	}
	rawEvents, rawErr := store.ReadEvents(sessionID)
	if rawErr != nil {
		return nil, err
	}
	log.Printf("zero sessions: failed to rehydrate compaction events for %s; falling back to raw events: %v", sessionID, err)
	return rawEvents, nil
}

func FormatExecPrompt(prompt string, prepared PreparedExec) string {
	if prepared.Mode == ModeNew || len(prepared.ContextEvents) == 0 {
		return prompt
	}
	events := promptContextEvents(prepared.ContextEvents)

	lines := []string{}
	for _, event := range events {
		lines = append(lines, fmt.Sprintf("- #%d %s: %s", event.Sequence, event.Type, summarizePayload(event.Payload)))
	}
	label := "Continuing"
	sessionID := prepared.Session.SessionID
	if prepared.Mode == ModeFork {
		label = "Forked from"
		if prepared.Session.ParentSessionID != "" {
			sessionID = prepared.Session.ParentSessionID
		}
	}
	return strings.Join([]string{
		fmt.Sprintf("%s Zero session %s.", label, sessionID),
		"Previous session context:",
		strings.Join(lines, "\n"),
		"",
		"Current user request:",
		prompt,
	}, "\n")
}

// promptContextEvents chooses what a resumed turn is told about the session so
// far.
//
// A RESUMED TURN NEEDS TO KNOW WHAT THE PREVIOUS ONE DID, NOT ONLY WHAT IT SAID.
//
// Conversation events alone were selected here, so a turn that read six files
// and then died before answering left this behind:
//
//   - #1 message: add retries to the http client
//   - #6 error: provider error: upstream timeout
//
// Nothing names the files, so the next turn re-reads them from scratch (#913).
// On a turn that ENDS NORMALLY the assistant's answer describes the work, which
// is why this was survivable; an interrupted turn produces no such answer, and
// the record of the work goes with it.
//
// ONLY THE UNSUMMARIZED TAIL, NOT EVERY TOOL EVENT. Filtering tool events out
// was deliberate (#460) and is right for work the assistant has already
// described: forty read_file results add length and no information next to the
// answer that explains them. It is wrong only for work with no answer after it,
// which is precisely what an interrupted turn leaves behind. So the events after
// the last assistant message come along, and everything the assistant already
// spoke for stays filtered.
//
// The tail also gets its own allowance rather than sharing the conversation
// budget, so a tool-heavy interrupted turn can never push an earlier message out
// of the context. That is the property #460 added this filter for and it still
// holds.
func promptContextEvents(events []Event) []Event {
	const maxPromptContextEvents = 80
	// Enough to describe an interrupted turn's work, small enough that the
	// conversation still dominates the prompt.
	const maxPromptContextTailEvents = 24

	lastSpoken := -1
	for index, event := range events {
		if event.Type == EventMessage && payloadRole(event.Payload) == "assistant" {
			lastSpoken = index
		}
	}

	conversation := make([]Event, 0, len(events))
	tail := make([]Event, 0, maxPromptContextTailEvents)
	for index, event := range events {
		switch event.Type {
		case EventMessage, EventCompaction, EventSessionFork, EventSessionChild, EventSpecialistStart, EventSpecialistStop, EventError:
			conversation = append(conversation, event)
		case EventToolCall:
			if index > lastSpoken {
				tail = append(tail, toolCallIdentity(event))
			}
		case EventToolResult:
			if index > lastSpoken {
				tail = append(tail, toolResultOutcome(event))
			}
		}
	}
	if len(conversation) == 0 && len(tail) == 0 {
		// An unrecognized event stream still says more than nothing.
		conversation = append(conversation, events...)
	}
	if len(conversation) > maxPromptContextEvents {
		conversation = conversation[len(conversation)-maxPromptContextEvents:]
	}
	// Tail slots exist only when the conversation uses fewer than the 80-event
	// cap. A session whose conversation already fills it carries no interrupted
	// tool work, which is what every session did before tool events were admitted
	// at all, and what the #460 cap is there to hold. Reserving a minimum tail by
	// trimming conversation further is a product decision, not this change.
	tailBudget := min(maxPromptContextTailEvents, maxPromptContextEvents-len(conversation))
	if tailBudget < 0 {
		tailBudget = 0
	}
	if len(tail) > tailBudget {
		tail = tail[len(tail)-tailBudget:]
	}
	if len(tail) == 0 {
		return conversation
	}
	// Merged back into execution order: the sequence numbers are rendered, so a
	// list that jumps backwards would read as a corrupted history.
	merged := make([]Event, 0, len(conversation)+len(tail))
	merged = append(merged, conversation...)
	merged = append(merged, tail...)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Sequence < merged[j].Sequence })
	return merged
}

// toolCallIdentityKeys are the argument fields that say WHAT a call was about
// without carrying what it was about to write, run, or search for.
//
// Path, directory, URL, name and pattern fields identify the work: the file that
// was read, the tree that was listed, the expression that was searched. Body
// fields carry payload: write_file's content, edit_file's old and new strings,
// apply_patch's hunks, a shell command with whatever credential was on its
// line. The result side already drops payload through toolResultOutcome, and
// admitting calls into resume context without the same care put an interrupted
// write_file's content into the next turn's prompt while its result body did
// not. Same fact, one door left open.
//
// AN ALLOW-LIST, NOT A DENY-LIST, so an argument this file has never heard of is
// dropped rather than replayed. A new tool with a new body field is then a
// missing path in a resume prompt, which is visible, instead of a new leak, which
// is not. The keys cover every alias the tools accept for the identity fields.
var toolCallIdentityKeys = map[string]bool{
	// files and directories
	"path": true, "file": true, "file_path": true, "filepath": true, "filename": true,
	"dir": true, "directory": true, "cwd": true, "workdir": true,
	// what a search was for; these are what the interrupted turn was looking at
	"pattern": true, "glob": true, "query": true, "regex": true, "expression": true,
	// fetches and named resources
	"url": true, "name": true,
	// read windows, so a resumed turn knows which part it already had
	"offset": true, "limit": true,
}

// toolCallIdentity keeps a tool call's identity and drops its payload, the
// symmetric half of toolResultOutcome.
//
// The arguments travel as a JSON string inside the payload. They are decoded,
// reduced to the identity keys, and re-encoded; anything that does not decode as
// an object is removed outright rather than passed through as text, since text
// that could not be read is text that cannot be checked.
func toolCallIdentity(event Event) Event {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &decoded); err != nil {
		event.Payload = json.RawMessage(`{}`)
		return event
	}
	raw, present := decoded["arguments"]
	if !present {
		return event
	}
	kept := map[string]any{}
	var argumentsText string
	if err := json.Unmarshal(raw, &argumentsText); err == nil {
		var arguments map[string]any
		if err := json.Unmarshal([]byte(argumentsText), &arguments); err == nil {
			for key, value := range arguments {
				if toolCallIdentityKeys[strings.ToLower(key)] {
					kept[key] = value
				}
			}
		}
	}
	if len(kept) == 0 {
		delete(decoded, "arguments")
	} else {
		reduced, err := json.Marshal(kept)
		if err != nil {
			delete(decoded, "arguments")
		} else {
			quoted, _ := json.Marshal(string(reduced))
			decoded["arguments"] = quoted
		}
	}
	rebuilt, err := json.Marshal(decoded)
	if err != nil {
		event.Payload = json.RawMessage(`{}`)
		return event
	}
	event.Payload = json.RawMessage(rebuilt)
	return event
}

// toolResultOutcome strips a tool result down to WHICH tool ran and HOW IT
// ENDED, dropping the output body.
//
// The tail exists so a resumed turn knows what the interrupted one did, and the
// CALL already carries that: the tool name and its arguments, which is the path
// for a read. The result adds only a 500-byte prefix of the output, which is
// worth little beside the call and is the one part of an event that can carry
// file contents. Nothing redacts on the way into a prompt, so keeping it would
// re-emit raw tool output into a later turn on the strength of a truncation
// limit alone.
//
// The status stays, because dropping it would be worse than dropping the whole
// result: a bare call reads as work that succeeded, so a failed read would come
// back as a file the next turn believes it already has.
func toolResultOutcome(event Event) Event {
	var decoded struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(event.Payload, &decoded); err != nil {
		// Undecodable: drop the payload rather than pass an unknown shape through.
		event.Payload = json.RawMessage(`{}`)
		return event
	}
	trimmed, err := json.Marshal(map[string]string{
		"name":   decoded.Name,
		"status": decoded.Status,
	})
	if err != nil {
		event.Payload = json.RawMessage(`{}`)
		return event
	}
	event.Payload = json.RawMessage(trimmed)
	return event
}

// payloadRole reads the "role" field of a message payload, returning "" when the
// payload is not a decodable object or carries no role.
func payloadRole(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var decoded struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(decoded.Role)
}

func forkTitle(title string) string {
	if title == "" {
		return ""
	}
	return title + " (fork)"
}

func summarizePayload(payload any) string {
	text := extractText(payload)
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		data, err := json.Marshal(payload)
		if err != nil {
			return "{}"
		}
		text = string(data)
	}
	if len(text) > 500 {
		return truncateUTF8(text, 500)
	}
	return text
}

// truncateUTF8 returns the longest prefix of s that is at most n bytes,
// backing off to the nearest rune boundary so a multi-byte character isn't
// split — a split rune here would embed invalid UTF-8 into the exec prompt.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func extractText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err == nil {
			return extractText(decoded)
		}
		return string(typed)
	case float64, bool, int:
		return fmt.Sprint(typed)
	case []any:
		parts := []string{}
		for _, item := range typed {
			if text := extractText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if summary, ok := typed["summary"].(string); ok && strings.TrimSpace(summary) != "" {
			return summary
		}
		parts := []string{}
		for _, item := range typed {
			if text := extractText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}
