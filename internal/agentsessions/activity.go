package agentsessions

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

// An imported session's tool work never reaches the model.
//
// sessions.promptContextEvents — the filter behind both `zero exec --resume` and
// the TUI's /resume — passes only EventMessage, EventCompaction, EventError and
// the session-lifecycle types. EventToolCall and EventToolResult are not in that
// list, so every file the other agent read, every command it ran and every error
// it hit is visible in the transcript and invisible to the model continuing the
// work. On a real 22-event import that left 2 messages and 1,155 characters of
// context.
//
// This file closes that gap without touching the shared filter: while
// translating, it records what the tools actually did and emits the result as
// EventCompaction, a type the filter already passes.
//
// Zero's own compaction was the obvious alternative and is strictly worse here:
// sessions.toolPayloadPreview allow-lists id/name/toolName/status and drops
// "arguments" and "output", so a compaction summariser learns that a Read failed
// but never which file or why. At translation time those values are still in
// hand.

// maxSummaryEventChars keeps one summary event inside the digest's per-event
// budget. sessions.summarizePayload truncates every event to 500 characters, so
// a single long summary would be sliced mid-sentence; several short ones each
// arrive intact. The margin absorbs the payload's own framing.
const maxSummaryEventChars = 460

// maxSummaryItems bounds one line's list before it collapses to a count. A
// session that touched ninety files should say so rather than name eleven of
// them and imply that was all.
const maxSummaryItems = 12

// activityLog accumulates what another agent's tools did during a translation.
// It is filled from the same pass that builds the events, so it costs no extra
// read of the transcript.
type activityLog struct {
	cwd string

	calls  int
	failed int

	read     []string
	changed  []string
	commands []string
	searches []string
	failures []string

	// pendingPath maps a call id to the bucket and value it contributed, so a
	// call that turns out to have FAILED can be withdrawn. Without this a Read
	// of a path that does not exist still appears under "Files read", which
	// reads as fact and is exactly the wrong thing to tell the next model.
	pendingPath map[string]pathClaim

	// toolCounts is the fallback when nothing could be extracted from a tool's
	// arguments: naming the tools and how often they ran is still true, where
	// guessing at a filename would not be.
	toolCounts map[string]int

	seen map[string]bool
}

// pathClaim is a file path a call contributed, remembered until its result is
// known.
type pathClaim struct {
	bucket string
	value  string
}

func newActivityLog(cwd string) *activityLog {
	return &activityLog{
		cwd:         cwd,
		toolCounts:  map[string]int{},
		seen:        map[string]bool{},
		pendingPath: map[string]pathClaim{},
	}
}

// withdraw removes a value a failed call had contributed.
func (log *activityLog) withdraw(claim pathClaim) {
	list := &log.read
	if claim.bucket == "changed" {
		list = &log.changed
	}
	for index, value := range *list {
		if value == claim.value {
			*list = append((*list)[:index], (*list)[index+1:]...)
			break
		}
	}
	delete(log.seen, claim.bucket+"\x00"+claim.value)
}

// add appends value to list unless an equal value is already recorded under
// bucket. Deduplicated because agents re-read the same file repeatedly and a
// list of forty identical paths tells the reader nothing.
func (log *activityLog) add(bucket string, list *[]string, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	key := bucket + "\x00" + trimmed
	if log.seen[key] {
		return
	}
	log.seen[key] = true
	*list = append(*list, trimmed)
}

// observeCall records one tool invocation.
//
// Classification is by ARGUMENT KEY, not by tool name. Tool names differ across
// agents and versions (Read/read_file/view, Bash/exec/shell), but the parameter
// carrying a path is reliably called some spelling of "path", and a command is
// reliably "command". Keys are allow-listed rather than sniffed: an unknown
// schema falls back to the tool-name count instead of guessing which field is a
// filename (repo invariant #2, and #8 — these arguments are untrusted input).
func (log *activityLog) observeCall(callID string, name string, arguments string) {
	log.calls++
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "unknown"
	}

	fields := map[string]any{}
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		// Not a JSON object. Codex's custom_tool_call carries a bare script in
		// "input", which is a command in every sense that matters here.
		if script := strings.TrimSpace(arguments); script != "" {
			log.add("cmd", &log.commands, log.shorten(firstLine(script)))
			return
		}
		log.toolCounts[trimmedName]++
		return
	}

	if command := firstStringField(fields, "command", "cmd", "script", "input"); command != "" {
		log.add("cmd", &log.commands, log.shorten(firstLine(command)))
		return
	}
	if pattern := firstStringField(fields, "pattern", "query", "search_query", "regex"); pattern != "" {
		log.add("search", &log.searches, log.shorten(pattern))
		return
	}
	if path := firstStringField(fields, "file_path", "filePath", "path", "notebook_path", "target_file"); path != "" {
		bucket, list := "read", &log.read
		if isMutatingToolName(trimmedName) {
			bucket, list = "changed", &log.changed
		}
		value := log.relative(path)
		log.add(bucket, list, value)
		log.pendingPath[callID] = pathClaim{bucket: bucket, value: value}
		return
	}
	log.toolCounts[trimmedName]++
}

// observeResult records a tool's outcome. Only failures are kept: a successful
// result is already implied by the call, while a failure is the single most
// useful thing to carry forward — it is what the next model would otherwise
// repeat.
func (log *activityLog) observeResult(callID string, name string, status tools.Status, output string) {
	if status != tools.StatusError {
		delete(log.pendingPath, callID)
		return
	}
	// The call did not do what it claimed, so withdraw the claim.
	if claim, ok := log.pendingPath[callID]; ok {
		log.withdraw(claim)
		delete(log.pendingPath, callID)
	}
	log.failed++
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "unknown"
	}
	detail := log.shorten(firstLine(output))
	if detail == "" {
		detail = "failed"
	}
	log.add("fail", &log.failures, trimmedName+": "+detail)
}

// isMutatingToolName reports whether a tool that takes a path changes the file.
// Substring matching on the name is deliberate: it holds across Write/write_file/
// FileWrite/apply_patch without a per-agent table to keep in sync.
func isMutatingToolName(name string) bool {
	lowered := strings.ToLower(name)
	for _, marker := range []string{"write", "edit", "patch", "create", "append", "insert", "replace", "delete", "remove"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// relative shortens an absolute path against the session's working directory,
// so a summary reads "internal/tui/model.go" instead of consuming a third of its
// character budget on a repeated home-directory prefix. A path outside the
// workspace keeps its absolute form — that it lies elsewhere is the interesting
// part.
func (log *activityLog) relative(path string) string {
	trimmed := strings.TrimSpace(path)
	root := strings.TrimSpace(log.cwd)
	if trimmed == "" || root == "" {
		return log.shorten(trimmed)
	}
	if relative, err := filepath.Rel(root, trimmed); err == nil && !strings.HasPrefix(relative, "..") {
		return log.shorten(relative)
	}
	return log.shorten(trimmed)
}

// shorten caps a single item so one long value cannot crowd out the rest of its
// line.
func (log *activityLog) shorten(value string) string {
	const limit = 120
	collapsed := strings.Join(strings.Fields(value), " ")
	runes := []rune(collapsed)
	if len(runes) <= limit {
		return collapsed
	}
	return string(runes[:limit]) + "…"
}

func firstLine(value string) string {
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		return value[:index]
	}
	return value
}

// firstStringField returns the first non-empty string among the named keys.
func firstStringField(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// summaryEvents renders the log as EventCompaction events, one per category.
//
// Several small events rather than one large one, because the resume digest
// truncates each event at 500 characters — a single combined summary would lose
// its tail. Failures come last so they sit closest to the user's new request.
func (log *activityLog) summaryEvents() []sessions.AppendEventInput {
	if log == nil || log.calls == 0 {
		return nil
	}

	events := []sessions.AppendEventInput{}
	headline := "Prior session activity: " + countPhrase(log.calls, "tool call")
	if log.failed > 0 {
		headline += ", " + countPhrase(log.failed, "failure")
	}
	headline += "."
	if extra := log.toolBreakdown(); extra != "" {
		headline += " " + extra
	}
	events = append(events, noteEvent(headline))

	for _, section := range []struct {
		label string
		items []string
	}{
		{"Files read", log.read},
		{"Files changed", log.changed},
		{"Commands run", log.commands},
		{"Searched for", log.searches},
		{"Failures", log.failures},
	} {
		if line := summaryLine(section.label, section.items); line != "" {
			events = append(events, noteEvent(line))
		}
	}
	return events
}

// toolBreakdown names tools whose arguments yielded nothing, so an unrecognised
// schema degrades to "Also: exec x4" rather than to silence.
func (log *activityLog) toolBreakdown() string {
	if len(log.toolCounts) == 0 {
		return ""
	}
	names := make([]string, 0, len(log.toolCounts))
	for name := range log.toolCounts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		count := log.toolCounts[name]
		if count > 1 {
			parts = append(parts, name+" x"+itoaEvents(count))
			continue
		}
		parts = append(parts, name)
	}
	return truncateToBudget("Also: "+strings.Join(parts, ", "), maxSummaryEventChars)
}

// summaryLine renders one category, collapsing to a count once the list grows
// past what is useful or past the character budget. The overflow is always
// stated: a truncated list that looks complete is how a reader concludes the
// other agent touched four files when it touched forty.
func summaryLine(label string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	kept := items
	dropped := 0
	if len(kept) > maxSummaryItems {
		dropped = len(kept) - maxSummaryItems
		kept = kept[:maxSummaryItems]
	}
	for {
		line := label + ": " + strings.Join(kept, ", ")
		if dropped > 0 {
			line += " (+" + itoaEvents(dropped) + " more)"
		}
		if len([]rune(line)) <= maxSummaryEventChars || len(kept) <= 1 {
			return truncateToBudget(line, maxSummaryEventChars)
		}
		dropped++
		kept = kept[:len(kept)-1]
	}
}

func truncateToBudget(value string, budget int) string {
	runes := []rune(value)
	if len(runes) <= budget {
		return value
	}
	return string(runes[:budget-1]) + "…"
}

func countPhrase(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return itoaEvents(count) + " " + noun + "s"
}
