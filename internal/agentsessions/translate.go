package agentsessions

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

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

// redact runs secret redaction AND control-stripping on imported text. Every
// field a foreign transcript supplies routes through here — the content-bearing
// ones and the structural ones (role, name, toolCallId) alike — because a
// malicious transcript can hide a credential in any of them and Zero then
// persists and renders it. This is the redaction chokepoint (invariant #6): no
// imported byte reaches a picker row or transcript line as a secret or as a live
// control sequence.
// THE ORDER IS THE WHOLE GUARANTEE. Strip first, then match.
//
// RedactString matches secrets by SHAPE, and stripControl deletes a control byte
// without leaving a gap, so it is also a REASSEMBLER. Running it second meant a
// transcript could split a credential with a NUL, an ESC or any C1 byte, sail
// past the shape patterns because neither half looks like a key, and then have
// the halves rejoined on the way out. Every shape leaked that way: sk-ant-,
// ghp_, AKIA. Normalizing first means the patterns see the text the reader will
// see, which is the only text worth matching against.
//
// Same defect as #835, where an MCP failure reason was redacted before the
// terminal sanitizer rejoined its halves. Any normalizer that removes bytes
// without leaving a gap has to run BEFORE whatever matches on them.
func redact(value string) string {
	if value == "" {
		return ""
	}
	return redaction.RedactString(stripControl(value), redaction.Options{})
}

// stripControl removes terminal control bytes from imported text. A foreign
// transcript is untrusted input (invariant #8): an ESC or NUL a title or
// message carries repaints or corrupts the terminal once it lands in a picker
// row or a transcript line — the class shipped in #835 (a forged row) and #876
// (a NUL that panicked the TUI). Tab and newline are kept because a transcript
// legitimately carries them; every other C0 byte, DEL, and C1 byte is dropped.
func stripControl(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		// FORMAT CHARACTERS ARE NOT CONTROL CHARACTERS, and unicode.IsControl
		// says so — but U+202E RIGHT-TO-LEFT OVERRIDE reorders everything after
		// it, so a tool name or title can be made to render as something else
		// entirely while the bytes stay innocent. Category Cf is invisible by
		// definition; nothing in a transcript needs it.
		case unicode.Is(unicode.Cf, r):
			return -1
		default:
			return r
		}
	}, value)
}

func messageEvent(role string, content string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type: sessions.EventMessage,
		Payload: map[string]any{
			"role":    redact(role),
			"content": redact(content),
		},
	}
}

type importCallIdentities struct {
	key []byte
}

func (identities *importCallIdentities) opaque(foreign string) string {
	if len(identities.key) == 0 {
		identities.key = []byte(rand.Text())
	}
	digest := hmac.New(sha256.New, identities.key)
	_, _ = digest.Write([]byte(foreign))
	return fmt.Sprintf("import-call-%x", digest.Sum(nil))
}

func toolCallEvent(identities *importCallIdentities, name string, foreignCallID string, arguments string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type: sessions.EventToolCall,
		Payload: map[string]any{
			"name": redact(name),
			// Identity is structural, not display text. Foreign ids may contain
			// secrets, and redaction is deliberately many-to-one, so persisting a
			// redacted foreign id can collapse distinct call/result pairs. This
			// per-import opaque id is non-secret and one-to-one.
			"toolCallId": identities.opaque(foreignCallID),
			"arguments":  redact(arguments),
		},
	}
}

func toolResultEvent(identities *importCallIdentities, name string, foreignCallID string, status tools.Status, output string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type: sessions.EventToolResult,
		Payload: map[string]any{
			"name":       redact(name),
			"toolCallId": identities.opaque(foreignCallID),
			"status":     string(status),
			"output":     redact(output),
		},
	}
}

// noteEventSummaryKey marks a message as a Zero-generated activity summary
// rather than a translated foreign-transcript turn. The TUI and the resume
// digest read only "role" and "content", so this key is invisible to render and
// to the model; it exists so a consumer that wants the imported transcript alone
// can tell the two apart. NoteEventIsSummary reads it.
const noteEventSummaryKey = "importedActivitySummary"

// NoteEventIsSummary reports whether an event is a Zero-generated activity
// summary message (see noteEvent) rather than a translated transcript turn. It
// takes any so callers can pass an AppendEventInput.Payload directly.
func NoteEventIsSummary(payload any) bool {
	m, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	flag, _ := m[noteEventSummaryKey].(bool)
	return flag
}

// noteEvent carries an imported-session activity summary as an assistant
// message. NOT EventCompaction: that type has a second contract on the replay
// side. RehydrateEvents restructures the transcript around the last
// EventCompaction, and an activity summary with no CompactionPayload bookkeeping
// (no CompactableEvents, CompactedThroughSequence 0) makes rehydration hoist
// this note to the FRONT of the transcript. EventMessage still passes
// promptContextEvents — the resume digest — without that restructuring. The
// summary marker keeps it distinguishable from a real assistant turn.
func noteEvent(summary string) sessions.AppendEventInput {
	return sessions.AppendEventInput{
		Type: sessions.EventMessage,
		Payload: map[string]any{
			"role":              "assistant",
			"content":           redact(summary),
			noteEventSummaryKey: true,
		},
	}
}

// translateFamily1 converts a family-1 transcript into Zero events.
//
// The mapping is deliberately lossy in one direction only: everything that
// affects what a reader (human or model) needs in order to continue the work is
// kept, and everything that belongs to the other model's private machinery is
// dropped. Zero's own resume renders these events to a text digest anyway
// (sessions.FormatExecPrompt), so perfect structural fidelity would buy nothing.
func translateFamily1(root string, path string, options ReadOptions) ([]sessions.AppendEventInput, error) {
	events := newEventTail(effectiveMaxEvents(options.MaxEvents))
	// A tool result names only the id of the call it answers, so the call's name
	// has to be carried forward. Every family-1 agent writes the tool_use before
	// the matching tool_result, so this is populated by the time it is read.
	toolNames := map[string]string{}
	identities := &importCallIdentities{}
	activity := newActivityLog(options.Cwd)

	omitted := 0
	prefixOmitted, err := streamTailLines(root, path, importLineLimit, importByteLimit, func(line []byte, truncated bool) bool {
		// A RECORD TOO LONG EVEN FOR THE IMPORT CAP IS REPORTED, NOT DROPPED.
		// Skipping it silently produced a transcript that looked complete: a
		// question, no answer, then the follow-up. The marker is the honest
		// answer — the bytes are gone either way, but the reader can see it.
		if truncated {
			omitted++
			return true
		}
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
				events.add(messageEvent(roleFor(record), text))
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
					events.add(messageEvent(roleFor(record), block.Text))
				}
			case "thinking":
				// The other model's reasoning. Dropped by default: it is private
				// to that provider, frequently larger than the visible
				// conversation, and a different model continuing this work will
				// not be picking up that chain of thought.
				if options.IncludeReasoning && strings.TrimSpace(block.Thinking) != "" {
					events.add(messageEvent("reasoning", block.Thinking))
				}
			case "tool_use":
				toolNames[block.ID] = block.Name
				activity.observeCall(block.ID, block.Name, string(block.Input))
				events.add(toolCallEvent(identities, block.Name, block.ID, string(block.Input)))
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
				events.add(toolResultEvent(identities, name, block.ToolUseID, status, output))
				delete(toolNames, block.ToolUseID)
			}
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	// SAID OUT LOUD. A resumed conversation that quietly lost a record reads as
	// complete to both the user and the model continuing it — the failure this
	// makes visible is a question with no answer followed by a follow-up.
	contextEvents := activity.summaryEvents()
	if prefixOmitted {
		contextEvents = append(contextEvents, omittedPrefixEvent())
	}
	if omitted > 0 {
		contextEvents = append(contextEvents, omittedRecordsEvent(omitted))
	}
	return capTranslatedEventsDropped(events.values(), contextEvents, effectiveMaxEvents(options.MaxEvents), events.dropped), nil
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
	return capEventsDropped(events, max, 0)
}

func capEventsDropped(events []sessions.AppendEventInput, max int, alreadyDropped int) []sessions.AppendEventInput {
	if alreadyDropped == 0 && (max <= 0 || len(events) <= max) {
		return events
	}
	// The note itself occupies one of the max slots, so one more original event
	// (the oldest of the tail) is dropped to make room for it. The reported
	// count must include that event: len(events)-max alone understates the loss
	// by one, and a truncation that reads as smaller than it was is how someone
	// concludes the other agent did less than it did.
	shown := events[len(events)-max+1:]
	baseShown := len(shown)
	shown, orphaned := withoutOrphanToolResults(shown)
	dropped := alreadyDropped + len(events) - baseShown + orphaned
	out := make([]sessions.AppendEventInput, 0, max)
	out = append(out, noteEvent(plural(dropped, "earlier event")+
		" from this session were not imported; the most recent "+
		itoaEvents(len(shown))+" are shown."))
	return append(out, shown...)
}

// capTranslatedEvents applies MaxEvents without allowing generated summaries
// to evict the actual transcript tail. Context receives spare/reserved slots,
// but at least the final source event always survives when source exists.
func capTranslatedEventsDropped(source, contextEvents []sessions.AppendEventInput, max int, alreadyDropped int) []sessions.AppendEventInput {
	if alreadyDropped == 0 && (max <= 0 || len(source)+len(contextEvents) <= max) {
		return append(append([]sessions.AppendEventInput{}, source...), contextEvents...)
	}
	if len(source) == 0 {
		return capEvents(contextEvents, max)
	}
	if len(contextEvents) == 0 {
		if max == 1 {
			// A one-event budget cannot hold both a disclosure and source content.
			// Preserve the promised transcript tail instead of returning only the
			// generated omission marker.
			return []sessions.AppendEventInput{source[len(source)-1]}
		}
		return capEventsDropped(source, max, alreadyDropped)
	}
	contextSlots := min(len(contextEvents), max-1)
	if contextSlots < 0 {
		contextSlots = 0
	}
	sourceSlots := max - contextSlots
	// If source must be truncated and the budget has room, reserve a second
	// source slot for capEvents' disclosure note. Keeping only the final source
	// event would satisfy the tail guarantee while silently hiding that earlier
	// transcript events were dropped.
	if len(source) > sourceSlots && max >= 2 && sourceSlots < 2 {
		sourceSlots = 2
		contextSlots = max - sourceSlots
	}
	var keptSource []sessions.AppendEventInput
	if sourceSlots <= 1 {
		// A one-event budget cannot hold both a disclosure and source content.
		// The flag promises transcript-tail events, so the final source event wins;
		// larger budgets retain the explicit omission marker below.
		keptSource = append(keptSource, source[len(source)-1])
	} else {
		keptSource = capEventsDropped(source, sourceSlots, alreadyDropped)
	}
	return append(keptSource, contextEvents[len(contextEvents)-contextSlots:]...)
}

const (
	defaultImportMaxEvents = 4096
	importByteLimit        = 32 << 20
)

func effectiveMaxEvents(requested int) int {
	if requested > 0 {
		return requested
	}
	return defaultImportMaxEvents
}

type eventTail struct {
	events  []sessions.AppendEventInput
	max     int
	start   int
	dropped int
}

func newEventTail(max int) *eventTail {
	return &eventTail{
		events: make([]sessions.AppendEventInput, 0, min(max, 128)),
		max:    max,
	}
}

func (tail *eventTail) add(event sessions.AppendEventInput) {
	if len(tail.events) < tail.max {
		tail.events = append(tail.events, event)
		return
	}
	tail.events[tail.start] = event
	tail.start = (tail.start + 1) % len(tail.events)
	tail.dropped++
}

func (tail *eventTail) values() []sessions.AppendEventInput {
	if tail.start == 0 {
		return tail.events
	}
	out := make([]sessions.AppendEventInput, 0, len(tail.events))
	out = append(out, tail.events[tail.start:]...)
	return append(out, tail.events[:tail.start]...)
}

func withoutOrphanToolResults(events []sessions.AppendEventInput) ([]sessions.AppendEventInput, int) {
	calls := map[string]bool{}
	out := make([]sessions.AppendEventInput, 0, len(events))
	dropped := 0
	for _, event := range events {
		payload, _ := event.Payload.(map[string]any)
		id, _ := payload["toolCallId"].(string)
		if event.Type == sessions.EventToolCall {
			calls[id] = true
		}
		if event.Type == sessions.EventToolResult {
			if !calls[id] {
				dropped++
				continue
			}
		}
		out = append(out, event)
	}
	return out, dropped
}

func omittedPrefixEvent() sessions.AppendEventInput {
	return noteEvent("Older transcript records were not imported; only a bounded tail of this foreign session was read.")
}

func itoaEvents(value int) string { return strconv.Itoa(value) }

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return itoaEvents(count) + " " + noun + "s"
}

// omittedRecordsEvent names what an import could not carry across.
//
// It is an EventError rather than a message because it is not part of the
// conversation and must not read as one: a model continuing this session should
// see a note about the transcript, not a turn somebody took. The count is the
// honest limit of what can be said — the bytes were never parsed, so their role,
// author and content are all unknown.
func omittedRecordsEvent(count int) sessions.AppendEventInput {
	noun := "record"
	if count != 1 {
		noun = "records"
	}
	return sessions.AppendEventInput{
		Type: sessions.EventError,
		Payload: map[string]any{
			"message": fmt.Sprintf("%d %s in the source transcript exceeded the import size limit and could not be read. "+
				"This imported conversation is missing that content.", count, noun),
		},
	}
}

// DisplayField makes one foreign metadata value safe to draw in a terminal.
//
// TWO SEPARATE HAZARDS, WITH REDACTION ON BOTH SIDES OF NORMALIZATION. The value is a field another product
// wrote into its own file: it can carry terminal escapes that repaint the rows
// around it, and it can carry something shaped like a credential — a title is
// often the user's first prompt, which is where a pasted key ends up.
//
// A pre-pass catches an intact secret immediately after a control/escape; then
// controls are stripped so a secret split by one cannot evade the post-pass.
// Layout goes as well, unlike the transcript
// helper, because a metadata field is drawn as one row and a newline in it moves
// the rest of the line somewhere the caller did not intend.
//
// TAB, NEWLINE AND RETURN BECOME A SPACE RATHER THAN VANISHING, and that gap is
// load-bearing in the opposite direction to the stripping above. Every secret
// pattern anchors on \b, so deleting the separator in "key<TAB>sk-ant-…" glued a
// word character onto the shape and the match no longer fired — a title is
// usually the user's first prompt, and a pasted key on the line after "key:" is
// exactly how one arrives. Substituting keeps the field one row while leaving the
// boundary the patterns need. It costs nothing that was being protected:
// redaction_order_test.go already establishes that a credential cannot contain a
// raw newline, so joining across one never reassembled a real secret. The
// invisible bytes — C0, DEL, C1, Cf — are still DELETED, because those are the
// ones an escape can hide inside a key.
func DisplayField(value string) string {
	// Redact once before normalization as well as after it. The first pass catches
	// an intact credential immediately following an escape/control sequence; the
	// second catches a credential whose bytes were split by controls and become
	// contiguous only after those controls are removed.
	value = redaction.RedactString(value, redaction.Options{})
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r == '\t' || r == '\n' || r == '\r' {
			b.WriteRune(' ')
			continue
		}
		// Cf as well as control: see stripControl. A bidi override in a picker row
		// reorders the rows's visible text without changing a byte of it.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return redaction.RedactString(strings.TrimSpace(b.String()), redaction.Options{})
}
