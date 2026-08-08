package agentsessions

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// payloadOf re-marshals an event payload the way the store will, so the tests
// assert on what actually lands in events.jsonl rather than on the Go map.
func payloadOf(t *testing.T, event sessions.AppendEventInput) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(event.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func keysOf(t *testing.T, event sessions.AppendEventInput) []string {
	t.Helper()
	keys := []string{}
	for key := range payloadOf(t, event) {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func str(t *testing.T, event sessions.AppendEventInput, key string) string {
	t.Helper()
	value, _ := payloadOf(t, event)[key].(string)
	return value
}

// TestPayloadKeysMatchWhatTheTUIReads is the guard on a layer join that nothing
// else checks: internal/tui/session.go's transcriptRowsFromSessionEvents reads
// these payloads by literal key name, and a mismatch produces a blank row with
// no error at either end. If that reader is ever renamed or its keys change,
// this test is the tripwire.
// conversationEvents drops the activity-summary events so a test asserting the
// SHAPE of the imported conversation is not perturbed by summaries being added
// or refined. Tests that care about the summary use summaryTexts instead.
func conversationEvents(events []sessions.AppendEventInput) []sessions.AppendEventInput {
	out := make([]sessions.AppendEventInput, 0, len(events))
	for _, event := range events {
		if event.Type == sessions.EventCompaction {
			continue
		}
		out = append(out, event)
	}
	return out
}

func TestPayloadKeysMatchWhatTheTUIReads(t *testing.T) {
	cases := []struct {
		name  string
		event sessions.AppendEventInput
		want  []string
	}{
		{"message", messageEvent("user", "hi"), []string{"content", "role"}},
		{"tool call", toolCallEvent("Read", "toolu_1", "{}"), []string{"arguments", "name", "toolCallId"}},
		{"tool result", toolResultEvent("Read", "toolu_1", "ok", "out"), []string{"name", "output", "status", "toolCallId"}},
		{"note", noteEvent("trimmed"), []string{"summary"}},
	}
	for _, test := range cases {
		got := keysOf(t, test.event)
		if strings.Join(got, ",") != strings.Join(test.want, ",") {
			t.Errorf("%s payload keys = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestAClaudeTranscriptBecomesZeroEvents(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","cwd":"/w","message":{"role":"user","content":"Find the bug"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[`+
			`{"type":"thinking","thinking":"private chain of thought"},`+
			`{"type":"text","text":"Looking now."},`+
			`{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"main.go"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"toolu_1","content":"package main"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Found it."}]}}`,
	)

	all, err := translateFamily1(path, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events := conversationEvents(all)

	wantTypes := []sessions.EventType{
		sessions.EventMessage,    // user prompt
		sessions.EventMessage,    // assistant text
		sessions.EventToolCall,   // Read
		sessions.EventToolResult, // its result
		sessions.EventMessage,    // assistant answer
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("event %d type = %s, want %s", i, events[i].Type, want)
		}
	}

	if got := str(t, events[0], "role"); got != "user" {
		t.Errorf("first event role = %q, want user", got)
	}
	if got := str(t, events[1], "role"); got != "assistant" {
		t.Errorf("second event role = %q, want assistant", got)
	}
	if got := str(t, events[2], "name"); got != "Read" {
		t.Errorf("tool call name = %q, want Read", got)
	}
	// The tool result names only an id; the name has to be carried from the call.
	if got := str(t, events[3], "name"); got != "Read" {
		t.Errorf("tool result name = %q, want the call's name carried forward", got)
	}
	if got := str(t, events[3], "output"); got != "package main" {
		t.Errorf("tool result output = %q", got)
	}
	// Reasoning is dropped by default.
	for _, event := range events {
		if strings.Contains(str(t, event, "content"), "private chain of thought") {
			t.Error("the other model's reasoning was imported despite IncludeReasoning being off")
		}
	}
}

// TestACallAndItsResultSharePairingID pins the property the TUI depends on to
// draw a call and its result as one exchange.
func TestACallAndItsResultSharePairingID(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_abc","name":"Bash","input":{"cmd":"ls"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_abc","content":"a.go"}]}}`,
	)
	events, err := translateFamily1(path, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events = conversationEvents(events)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	call, result := str(t, events[0], "toolCallId"), str(t, events[1], "toolCallId")
	if call == "" || call != result {
		t.Errorf("call id %q and result id %q must match and be non-empty", call, result)
	}
	if call != "toolu_abc" {
		t.Errorf("id = %q, want the foreign agent's own id reused verbatim", call)
	}
}

func TestAFailedToolCallKeepsItsErrorStatus(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"exit status 1"}]}}`,
	)
	events := conversationEvents(mustTranslate(t, path))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if got := str(t, events[1], "status"); got != "error" {
		t.Errorf("status = %q, want error — a failure that imports as a success "+
			"tells the next model the work succeeded", got)
	}
}

func TestReasoningIsKeptWhenAskedFor(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"weighing options"}]}}`,
	)
	if events, _ := translateFamily1(path, ReadOptions{}); len(events) != 0 {
		t.Errorf("got %d events by default, want reasoning dropped", len(events))
	}
	events, _ := translateFamily1(path, ReadOptions{IncludeReasoning: true})
	if len(events) != 1 || !strings.Contains(str(t, events[0], "content"), "weighing options") {
		t.Errorf("IncludeReasoning did not keep the reasoning block: %+v", events)
	}
}

// TestSecretsInAForeignTranscriptAreRedacted is the second load-bearing
// security test. These transcripts are another program's logs; whatever that
// program echoed into them is untrusted input the moment Zero copies it into
// its own event log.
func TestSecretsInAForeignTranscriptAreRedacted(t *testing.T) {
	const leaked = "sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKKKLLLL"
	path := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"my key is `+leaked+`"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"cmd":"export K=`+leaked+`"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"echoed `+leaked+`"}]}}`,
	)
	events, err := translateFamily1(path, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(conversationEvents(events)); got != 3 {
		t.Fatalf("got %d conversation events, want 3", got)
	}
	// Every field that carries free text must be scrubbed, not just the obvious one.
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), leaked) {
		t.Errorf("a secret survived translation into the event payloads:\n%s", encoded)
	}
	if !strings.Contains(string(encoded), "REDACTED") {
		t.Errorf("nothing was redacted at all; expected the redactor to fire:\n%s", encoded)
	}
}

func TestATruncatedTranscriptStillImportsWhatCameBefore(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"first"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"tor`, // torn
	)
	events, err := translateFamily1(path, ReadOptions{})
	if err != nil {
		t.Fatalf("a torn final line must not fail the import: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want the 2 complete records", len(events))
	}
}

func TestCappingKeepsTheTailAndSaysSo(t *testing.T) {
	lines := []string{}
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"type":"user","message":{"role":"user","content":"turn `+itoa(i)+`"}}`)
	}
	path := writeTranscript(t, lines...)

	events, err := translateFamily1(path, ReadOptions{MaxEvents: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 10 {
		t.Fatalf("got %d events, want exactly the 10 requested", len(events))
	}
	// The cap must never be silent: an import that looks complete but is not is
	// how someone concludes the other agent never did the work.
	if events[0].Type != sessions.EventCompaction {
		t.Errorf("first event = %s, want a note announcing the trim", events[0].Type)
	}
	if summary := str(t, events[0], "summary"); !strings.Contains(summary, "not imported") {
		t.Errorf("trim note = %q, want it to say events were dropped", summary)
	}
	// And what survives must be the END of the session, not the beginning.
	last := str(t, events[len(events)-1], "content")
	if last != "turn 49" {
		t.Errorf("last kept event = %q, want the final turn — the tail is what a "+
			"resume needs", last)
	}
}

func TestNoCapKeepsEverything(t *testing.T) {
	lines := []string{}
	for i := 0; i < 30; i++ {
		lines = append(lines, `{"type":"user","message":{"role":"user","content":"turn `+itoa(i)+`"}}`)
	}
	events, err := translateFamily1(writeTranscript(t, lines...), ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 30 {
		t.Errorf("got %d events, want all 30 when MaxEvents is unset", len(events))
	}
}

func TestReadRejectsAnUnknownSession(t *testing.T) {
	adapter := ClaudeCode(testEnv(t.TempDir(), nil))
	if _, err := adapter.Read("nope", ReadOptions{}); err == nil {
		t.Error("Read of an unknown id returned no error — the caller named a " +
			"specific session and an empty result would misrepresent it")
	}
}

func mustTranslate(t *testing.T, path string) []sessions.AppendEventInput {
	t.Helper()
	events, err := translateFamily1(path, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return events
}
