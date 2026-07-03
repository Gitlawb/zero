package specialist

import (
	"testing"

	"github.com/Gitlawb/zero/internal/streamjson"
)

// runDecoder feeds lines through a fresh decoder (as runChildWithDecoder
// would: decodeLine per non-empty line, then finish once at exit) and
// collects every emitted event, so decoder behavior is testable without
// spawning a real child process.
func runDecoder(decoder childDecoder, lines []string, exitCode int) []streamjson.Event {
	var events []streamjson.Event
	for _, line := range lines {
		events = append(events, decoder.decodeLine(line)...)
	}
	events = append(events, decoder.finish(exitCode)...)
	return events
}

func TestNewHarnessDecoderSelectsByStreamFormat(t *testing.T) {
	cases := map[string]childDecoder{
		"claude-stream-json": &claudeStreamDecoder{},
		"codex-json":         &codexJSONDecoder{},
		"text":               &textDecoder{},
		"gemini-json":        &textDecoder{}, // unrecognized -> text fallback
		"":                   &textDecoder{}, // unrecognized -> text fallback
	}
	for stream, want := range cases {
		t.Run(stream, func(t *testing.T) {
			got := newHarnessDecoder(stream)
			gotType := typeName(got)
			wantType := typeName(want)
			if gotType != wantType {
				t.Fatalf("newHarnessDecoder(%q) = %s, want %s", stream, gotType, wantType)
			}
		})
	}
}

func typeName(decoder childDecoder) string {
	switch decoder.(type) {
	case *claudeStreamDecoder:
		return "claude"
	case *codexJSONDecoder:
		return "codex"
	case *textDecoder:
		return "text"
	default:
		return "unknown"
	}
}

func TestClaudeStreamDecoderTextAndToolCallAndFinal(t *testing.T) {
	lines := []string{
		`{"type":"system","subtype":"init","cwd":"/tmp"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Looking at the file"},{"type":"tool_use","id":"call_1","name":"read_file","input":{"path":"a.go"}}]}}`,
		`{"type":"result","subtype":"success","result":"All done"}`,
	}
	events := runDecoder(&claudeStreamDecoder{}, lines, 0)

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %#v", len(events), events)
	}
	if events[0].Type != streamjson.EventText || events[0].Delta != "Looking at the file" {
		t.Fatalf("event 0 = %#v", events[0])
	}
	if events[1].Type != streamjson.EventToolCall || events[1].Name != "read_file" || events[1].ID != "call_1" {
		t.Fatalf("event 1 = %#v", events[1])
	}
	if args, ok := events[1].Args.(map[string]any); !ok || args["path"] != "a.go" {
		t.Fatalf("event 1 args = %#v, want map with path=a.go", events[1].Args)
	}
	if events[2].Type != streamjson.EventFinal || events[2].Text != "All done" {
		t.Fatalf("event 2 = %#v", events[2])
	}
}

func TestClaudeStreamDecoderTolerantOfMalformedAndUnknownLines(t *testing.T) {
	lines := []string{
		`not json at all`,
		`{"type":"ping"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
	}
	events := runDecoder(&claudeStreamDecoder{}, lines, 0)
	if len(events) != 1 || events[0].Type != streamjson.EventText || events[0].Delta != "hi" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestClaudeStreamDecoderNoSyntheticFinalWithoutResultLine(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}`,
	}
	events := runDecoder(&claudeStreamDecoder{}, lines, 1)
	for _, event := range events {
		if event.Type == streamjson.EventFinal {
			t.Fatalf("expected no synthetic final event, got %#v", events)
		}
	}
}

// The lines below are captured from a real run (codex-cli 0.142.5): `codex
// exec --json --skip-git-repo-check "reply with exactly: hi" < /dev/null`,
// with unrelated noise (thread.started, deprecation/plugin-config error
// items, MCP transport errors) stripped — see harness_decode.go's decoder doc
// comment for the untrimmed capture. The legacy `{"msg":{"type":...}}` shape
// these tests exercised before no longer appears in real codex output at all.
func TestCodexJSONDecoderAgentMessageAndTurnCompleted(t *testing.T) {
	lines := []string{
		`{"type":"item.completed","item":{"id":"item_a","type":"agent_message","text":"step one"}}`,
		`{"type":"item.completed","item":{"id":"item_b","type":"agent_message","text":"step two"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":27089,"cached_input_tokens":4992,"output_tokens":24,"reasoning_output_tokens":17}}`,
	}
	events := runDecoder(&codexJSONDecoder{}, lines, 0)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4: %#v", len(events), events)
	}
	if events[0].Type != streamjson.EventText || events[0].Delta != "step one" {
		t.Fatalf("event 0 = %#v", events[0])
	}
	if events[1].Type != streamjson.EventText || events[1].Delta != "step two" {
		t.Fatalf("event 1 = %#v", events[1])
	}
	if events[2].Type != streamjson.EventUsage || events[2].PromptTokens == nil || *events[2].PromptTokens != 27089 {
		t.Fatalf("event 2 = %#v", events[2])
	}
	if events[2].CompletionTokens == nil || *events[2].CompletionTokens != 24 {
		t.Fatalf("event 2 completion tokens = %#v", events[2])
	}
	if events[2].TotalTokens == nil || *events[2].TotalTokens != 27113 {
		t.Fatalf("event 2 total tokens = %#v, want input+output", events[2])
	}
	if events[3].Type != streamjson.EventFinal || events[3].Text != "step two" {
		t.Fatalf("event 3 = %#v, want final with last agent_message", events[3])
	}
}

func TestCodexJSONDecoderFinalizesOnLastMessageWhenStreamEndsWithoutTurnCompleted(t *testing.T) {
	lines := []string{
		`{"type":"item.completed","item":{"id":"item_a","type":"agent_message","text":"only message"}}`,
	}
	events := runDecoder(&codexJSONDecoder{}, lines, -1)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (text + synthesized final): %#v", len(events), events)
	}
	if events[1].Type != streamjson.EventFinal || events[1].Text != "only message" {
		t.Fatalf("event 1 = %#v", events[1])
	}
}

func TestCodexJSONDecoderIgnoresNonAgentMessageItemsAndMalformedLines(t *testing.T) {
	lines := []string{
		`{not json`,
		// Real noise seen in captures: deprecation/plugin-config warnings surface
		// as item.completed items of type "error", not the final answer.
		`{"type":"item.completed","item":{"id":"item_0","type":"error","message":"some config warning"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"reasoning","text":"thinking..."}}`,
		`{"type":"turn.started"}`,
	}
	events := runDecoder(&codexJSONDecoder{}, lines, 0)
	if len(events) != 0 {
		t.Fatalf("expected no events, got %#v", events)
	}
}

// TestCodexJSONDecoderRealCaptureEndToEnd replays the UNTRIMMED real capture
// (codex-cli 0.142.5, `codex exec --json --skip-git-repo-check "reply with
// exactly: hi" < /dev/null`) verbatim, including lines the legacy decoder
// never had to handle: a leading non-JSON stdin-prompt line, and mid-stream
// plain-text Rust log lines from failed MCP transport workers (these came out
// on stdout interleaved with the NDJSON, not stderr, in this capture). Every
// non-agent_message/turn.completed line must be silently ignored and the
// decoder must still land on exactly the real final answer and usage.
func TestCodexJSONDecoderRealCaptureEndToEnd(t *testing.T) {
	lines := []string{
		`Reading additional input from stdin...`,
		`{"type":"thread.started","thread_id":"019f2779-ba38-7782-8c13-5168ea9f776b"}`,
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"error\",\"message\":\"`[features].codex_hooks` is deprecated. Use `[features].hooks` instead.\"}}",
		`{"type":"item.completed","item":{"id":"item_1","type":"error","message":"failed to parse plugin hooks config"}}`,
		`{"type":"turn.started"}`,
		`2026-07-03T10:15:18.625718Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when AuthRequired(AuthRequiredError { www_authenticate_header: "Bearer error=\"invalid_token\"" })`,
		`2026-07-03T10:15:18.689968Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when AuthRequired(AuthRequiredError { www_authenticate_header: "Bearer resource_metadata=\"https://mcp.slack.com/.well-known/oauth-protected-resource\"" })`,
		`{"type":"item.completed","item":{"id":"item_2","type":"error","message":"Exceeded skills context budget of 2%."}}`,
		`{"type":"item.completed","item":{"id":"item_3","type":"agent_message","text":"hi"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":27089,"cached_input_tokens":4992,"output_tokens":24,"reasoning_output_tokens":17}}`,
	}
	events := runDecoder(&codexJSONDecoder{}, lines, 0)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (text + usage + final): %#v", len(events), events)
	}
	if events[0].Type != streamjson.EventText || events[0].Delta != "hi" {
		t.Fatalf("event 0 = %#v", events[0])
	}
	if events[1].Type != streamjson.EventUsage || events[1].PromptTokens == nil || *events[1].PromptTokens != 27089 {
		t.Fatalf("event 1 = %#v", events[1])
	}
	if events[2].Type != streamjson.EventFinal || events[2].Text != "hi" {
		t.Fatalf("event 2 = %#v, want final answer \"hi\"", events[2])
	}
}

func TestCodexJSONDecoderTurnCompletedWithoutUsageStillEmitsFinal(t *testing.T) {
	lines := []string{
		`{"type":"item.completed","item":{"id":"item_a","type":"agent_message","text":"done"}}`,
		`{"type":"turn.completed"}`,
	}
	events := runDecoder(&codexJSONDecoder{}, lines, 0)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (text + final, no usage event): %#v", len(events), events)
	}
	if events[1].Type != streamjson.EventFinal || events[1].Text != "done" {
		t.Fatalf("event 1 = %#v", events[1])
	}
}

func TestTextDecoderStreamsAndFinalizesOnCleanExit(t *testing.T) {
	lines := []string{"line one", "line two"}
	events := runDecoder(&textDecoder{}, lines, 0)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %#v", len(events), events)
	}
	if events[0].Type != streamjson.EventText || events[0].Delta != "line one\n" {
		t.Fatalf("event 0 = %#v", events[0])
	}
	if events[2].Type != streamjson.EventFinal || events[2].Text != "line one\nline two" {
		t.Fatalf("event 2 = %#v", events[2])
	}
}

func TestTextDecoderNoFinalOnNonZeroExit(t *testing.T) {
	events := runDecoder(&textDecoder{}, []string{"oops"}, 1)
	if len(events) != 1 || events[0].Type != streamjson.EventText {
		t.Fatalf("expected only the streamed text event, got %#v", events)
	}
}
