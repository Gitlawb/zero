package agentsessions

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

// The payload field names below are a CONTRACT with the TUI, not a convention.
// internal/tui/session.go's transcriptRowsFromSessionEvents reads exactly these
// keys ("role", "content", "name", "toolCallId", "arguments", "status",
// "output"); a misspelling renders an empty row and reports no error anywhere.
// Every event this package produces is built by one of the four constructors
// here so there is a single place for those names to be right.
//
// These constructors are also the redaction chokepoint (repo invariant #6).
// Imported text is untrusted input (invariant #8) — a foreign transcript can
// contain a key the other agent echoed into its own log — and routing every
// event through here means no future caller can add an unredacted path without
// deleting a call they can see.

func redact(value string) string {
	if value == "" {
		return ""
	}
	return redaction.RedactString(value, redaction.Options{})
}

func messageEvent(role string, content string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type: sessions.EventMessage,
		Payload: map[string]any{
			"role":    role,
			"content": redact(content),
		},
	}
}

func toolCallEvent(name string, callID string, arguments string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type: sessions.EventToolCall,
		Payload: map[string]any{
			"name": name,
			// The foreign agent's own call id is reused verbatim so a call and
			// its result pair up: the TUI keys them together on this string
			// (effectiveToolRowID), and inventing new ids would split every pair.
			"toolCallId": callID,
			"arguments":  redact(arguments),
		},
	}
}

func toolResultEvent(name string, callID string, status tools.Status, output string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type: sessions.EventToolResult,
		Payload: map[string]any{
			"name":       name,
			"toolCallId": callID,
			"status":     string(status),
			"output":     redact(output),
		},
	}
}

func noteEvent(summary string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type:    sessions.EventCompaction,
		Payload: map[string]any{"summary": redact(summary)},
	}
}

// translateFamily1 converts a family-1 transcript into Zero events.
//
// The mapping is deliberately lossy in one direction only: everything that
// affects what a reader (human or model) needs in order to continue the work is
// kept, and everything that belongs to the other model's private machinery is
// dropped. Zero's own resume renders these events to a text digest anyway
// (sessions.FormatExecPrompt), so perfect structural fidelity would buy nothing.
func translateFamily1(path string, options ReadOptions) ([]sessions.AppendEventInput, error) {
	events := []sessions.AppendEventInput{}
	// A tool result names only the id of the call it answers, so the call's name
	// has to be carried forward. Every family-1 agent writes the tool_use before
	// the matching tool_result, so this is populated by the time it is read.
	toolNames := map[string]string{}
	activity := newActivityLog(options.Cwd)

	err := streamLines(path, defaultHeadLimit.MaxLineBytes, func(line []byte) bool {
		var record family1Record
		if json.Unmarshal(line, &record) != nil || record.Message == nil {
			// Torn or unrecognised lines are skipped, not fatal: transcripts are
			// appended live and the final line is routinely half-written.
			return true
		}

		// Content is either a bare string (a plain user prompt) or an array of
		// typed blocks.
		var text string
		if json.Unmarshal(record.Message.Content, &text) == nil {
			if strings.TrimSpace(text) != "" {
				events = append(events, messageEvent(roleFor(record), text))
			}
			return true
		}

		var blocks []family1Block
		if json.Unmarshal(record.Message.Content, &blocks) != nil {
			return true
		}
		for _, block := range blocks {
			switch block.Type {
			case "text":
				if strings.TrimSpace(block.Text) != "" {
					events = append(events, messageEvent(roleFor(record), block.Text))
				}
			case "thinking":
				// The other model's reasoning. Dropped by default: it is private
				// to that provider, frequently larger than the visible
				// conversation, and a different model continuing this work will
				// not be picking up that chain of thought.
				if options.IncludeReasoning && strings.TrimSpace(block.Thinking) != "" {
					events = append(events, messageEvent("reasoning", block.Thinking))
				}
			case "tool_use":
				toolNames[block.ID] = block.Name
				activity.observeCall(block.ID, block.Name, string(block.Input))
				events = append(events, toolCallEvent(block.Name, block.ID, string(block.Input)))
			case "tool_result":
				name := toolNames[block.ToolUseID]
				if name == "" {
					name = "unknown"
				}
				status := tools.StatusOK
				if block.IsError {
					status = tools.StatusError
				}
				output := family1ResultText(block.Content)
				activity.observeResult(block.ToolUseID, name, status, output)
				events = append(events, toolResultEvent(name, block.ToolUseID, status, output))
			}
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	events = append(events, activity.summaryEvents()...)
	return capEvents(events, options.MaxEvents), nil
}

// roleFor maps a record to the role the TUI understands. Anything that is not
// user or assistant renders as a system row, which is the right home for the
// agent's own bookkeeping records.
func roleFor(record family1Record) string {
	if record.Message != nil && strings.TrimSpace(record.Message.Role) != "" {
		return strings.ToLower(record.Message.Role)
	}
	return strings.ToLower(record.Type)
}

// family1ResultText flattens a tool result's content, which may be a bare string
// or an array of blocks.
func family1ResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	if flattened := family1Text(raw); flattened != "" {
		return flattened
	}
	// Structured content with no text blocks (an image result, say). Keeping the
	// raw JSON is better than an empty row: the reader at least learns that the
	// call returned something and what shape it was.
	return string(raw)
}

// capEvents keeps the LAST max events, because the tail is what a resume needs
// — the most recent exchanges describe where the work actually stopped.
//
// The drop is announced rather than silent. A truncated import that looks
// complete is how someone concludes the other agent never did the work.
func capEvents(events []sessions.AppendEventInput, max int) []sessions.AppendEventInput {
	if max <= 0 || len(events) <= max {
		return events
	}
	dropped := len(events) - max
	kept := events[dropped:]
	// The note occupies one of the kept slots so the result never exceeds max.
	out := make([]sessions.AppendEventInput, 0, max)
	out = append(out, noteEvent(plural(dropped, "earlier event")+
		" from this session were not imported; the most recent "+
		itoaEvents(len(kept)-1)+" are shown."))
	return append(out, kept[1:]...)
}

func itoaEvents(value int) string { return strconv.Itoa(value) }

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return itoaEvents(count) + " " + noun + "s"
}
