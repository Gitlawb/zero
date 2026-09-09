package sessions

import (
	"fmt"
	"strings"
	"testing"
)

// A CALL'S PAYLOAD MUST NOT RIDE INTO A LATER PROMPT ANY MORE THAN A RESULT'S.
//
// toolResultOutcome drops result bodies, and calls were left verbatim, so an
// interrupted write_file put its content into the next turn's prompt while the
// matching result body did not. Same secret, one door left open. The identity
// still has to survive, since a resumed turn knowing WHICH file was written is
// the whole point of admitting calls at all.
//
// Pinned as whole lines, compared as fields, for the same reason the result test
// is: a substring search for one fixture secret passes when a truncated or
// reworded copy leaks, and the renderer walks a map so field order varies.
func TestResumePromptCarriesToolCallIdentityWithoutPayload(t *testing.T) {
	const secret = "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG"
	const token = "Bearer sk-live-4eC39HqLyjWDarjtT1zdp7dc"
	events := []Event{
		{Sequence: 1, Type: EventMessage, Payload: toolContextPayload(t, map[string]any{"role": "user", "content": "rotate the deploy key"})},
		{Sequence: 2, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c1", "name": "write_file", "arguments": `{"path":"deploy/prod.env","content":"` + secret + `"}`})},
		{Sequence: 3, Type: EventToolResult, Payload: toolContextPayload(t, map[string]any{"name": "write_file", "status": "ok", "output": "written"})},
		{Sequence: 4, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c2", "name": "edit_file", "arguments": `{"path":"deploy/prod.env","old_string":"` + secret + `","new_string":"rotated"}`})},
		{Sequence: 5, Type: EventToolResult, Payload: toolContextPayload(t, map[string]any{"name": "edit_file", "status": "ok"})},
		{Sequence: 6, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c3", "name": "exec_command", "arguments": `{"cmd":"curl -H '` + token + `' https://api.example/rotate","workdir":"deploy"}`})},
		{Sequence: 7, Type: EventToolResult, Payload: toolContextPayload(t, map[string]any{"name": "exec_command", "status": "ok"})},
		{Sequence: 8, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c4", "name": "apply_patch", "arguments": `{"patch":"*** Begin Patch\n*** Update File: deploy/prod.env\n-` + secret + `\n+rotated\n*** End Patch"}`})},
		{Sequence: 9, Type: EventToolResult, Payload: toolContextPayload(t, map[string]any{"name": "apply_patch", "status": "ok"})},
		{Sequence: 10, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c5", "name": "read_file", "arguments": `{"path":"deploy/prod.env","offset":1,"limit":40}`})},
		{Sequence: 11, Type: EventToolResult, Payload: toolContextPayload(t, map[string]any{"name": "read_file", "status": "ok", "output": secret})},
		{Sequence: 12, Type: EventError, Payload: toolContextPayload(t, map[string]any{"message": "provider error: upstream timeout"})},
	}
	out := resumePrompt(t, events)

	// The payloads: none of them, in any form.
	for _, leaked := range []string{secret, secret[:16], token, token[:14], "rotated", "Begin Patch", "curl -H"} {
		if strings.Contains(out, leaked) {
			t.Errorf("tool call payload %q reached the resume prompt:\n%s", leaked, out)
		}
	}

	// The identities: every one of them, from the call side.
	want := map[int]struct {
		kind   string
		fields []string
	}{
		2:  {"tool_call", []string{"c1", "write_file", `{"path":"deploy/prod.env"}`}},
		4:  {"tool_call", []string{"c2", "edit_file", `{"path":"deploy/prod.env"}`}},
		6:  {"tool_call", []string{"c3", "exec_command", `{"workdir":"deploy"}`}},
		8:  {"tool_call", []string{"c4", "apply_patch"}},
		10: {"tool_call", []string{"c5", "read_file"}},
	}
	seen := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "- #") || !strings.Contains(line, "tool_call:") {
			continue
		}
		var sequence int
		var kind string
		rest, _ := strings.CutPrefix(line, "- #")
		if _, err := fmt.Sscanf(rest, "%d %s", &sequence, &kind); err != nil {
			t.Fatalf("unparsable context line %q: %v", line, err)
		}
		expected, wanted := want[sequence]
		if !wanted {
			t.Errorf("unexpected tool_call line %q", line)
			continue
		}
		seen[sequence] = true
		_, payload, _ := strings.Cut(rest, ": ")
		fields := strings.Fields(payload)
		for _, field := range expected.fields {
			if !containsField(fields, field) {
				t.Errorf("line #%d lost identity field %q: %q", sequence, field, line)
			}
		}
	}
	for sequence := range want {
		if !seen[sequence] {
			t.Errorf("tool_call #%d did not reach the resume prompt at all, so the interrupted work is invisible again", sequence)
		}
	}
	// read_file keeps its window too, or a resumed turn re-reads a part it had.
	if !strings.Contains(out, `"offset":1`) || !strings.Contains(out, `"limit":40`) {
		t.Errorf("read_file lost its offset/limit window:\n%s", out)
	}
}

func containsField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	// The reduced arguments object may carry more than one identity key, in map
	// order, so a single-key expectation is also satisfied by an object that
	// contains that key.
	if strings.HasPrefix(want, "{") {
		key := strings.TrimSuffix(strings.TrimPrefix(want, "{"), "}")
		for _, field := range fields {
			if strings.HasPrefix(field, "{") && strings.Contains(field, key) {
				return true
			}
		}
	}
	return false
}

// ARGUMENTS THAT CANNOT BE READ ARE NOT PASSED THROUGH AS TEXT.
//
// An allow-list only protects what it can see. Arguments that do not decode as
// an object are dropped rather than rendered, since text this file could not
// inspect is text it cannot vouch for.
func TestResumePromptDropsUnreadableToolCallArguments(t *testing.T) {
	const secret = "sk-live-unparseable-9f8e7d6c"
	events := []Event{
		{Sequence: 1, Type: EventMessage, Payload: toolContextPayload(t, map[string]any{"role": "user", "content": "go"})},
		{Sequence: 2, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c1", "name": "bash", "arguments": `not json at all ` + secret})},
		{Sequence: 3, Type: EventError, Payload: toolContextPayload(t, map[string]any{"message": "provider error"})},
	}
	out := resumePrompt(t, events)
	if strings.Contains(out, secret) {
		t.Errorf("unparseable arguments were rendered as text:\n%s", out)
	}
	if !strings.Contains(out, "bash") {
		t.Errorf("the call's identity was lost along with its unreadable arguments:\n%s", out)
	}
}

// AND A TOOL THIS FILE HAS NEVER HEARD OF STILL KEEPS ITS PATH.
//
// The allow-list is by key, not by tool name, so an MCP tool or a future core
// tool whose argument is a path or a url carries it into the resume prompt
// without being enumerated here, while any body field it has is dropped.
func TestResumePromptKeepsIdentityForUnknownTools(t *testing.T) {
	const body = "large opaque payload that must not be replayed"
	events := []Event{
		{Sequence: 1, Type: EventMessage, Payload: toolContextPayload(t, map[string]any{"role": "user", "content": "go"})},
		{Sequence: 2, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c1", "name": "mcp_uploader", "arguments": `{"url":"https://files.example/x","blob":"` + body + `"}`})},
		{Sequence: 3, Type: EventError, Payload: toolContextPayload(t, map[string]any{"message": "provider error"})},
	}
	out := resumePrompt(t, events)
	if strings.Contains(out, body) {
		t.Errorf("an unlisted body field was replayed:\n%s", out)
	}
	if !strings.Contains(out, "https://files.example/x") {
		t.Errorf("an unlisted tool lost its url identity:\n%s", out)
	}
}

// AND AN ALLOW-LISTED KEY IS NOT A SAFE VALUE.
//
// web_fetch accepts a credential in the query string and redacts the URL it
// reports back, so an interrupted fetch had its token dropped from the result
// and kept verbatim in the call. This projection is what admits the call into a
// later turn's prompt, so the token rode into the next resume or fork. The
// identity the resumed turn actually needs is the host and path, and those
// survive.
func TestResumePromptRedactsCredentialsInsideRetainedValues(t *testing.T) {
	const token = "secret-value-9f8e7d6c5b4a"
	events := []Event{
		{Sequence: 1, Type: EventMessage, Payload: toolContextPayload(t, map[string]any{"role": "user", "content": "check the feed"})},
		{Sequence: 2, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c1", "name": "web_fetch", "arguments": `{"url":"https://api.example/data?access_token=` + token + `&page=2"}`})},
		{Sequence: 3, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c2", "name": "web_fetch", "arguments": `{"url":"https://reader:hunter2@api.example/feed"}`})},
		{Sequence: 4, Type: EventError, Payload: toolContextPayload(t, map[string]any{"message": "provider error: upstream timeout"})},
	}
	out := resumePrompt(t, events)

	for _, leaked := range []string{token, token[:12], "hunter2"} {
		if strings.Contains(out, leaked) {
			t.Errorf("credential %q reached the resume prompt:\n%s", leaked, out)
		}
	}
	// The non-secret identity is the whole reason calls are admitted at all.
	for _, kept := range []string{"api.example", "/data", "page=2", "/feed"} {
		if !strings.Contains(out, kept) {
			t.Errorf("URL identity %q was lost to the scrub:\n%s", kept, out)
		}
	}
}

// The scrub must not eat ordinary identities. A path, a glob and a query are
// what the resumed turn navigates by, and none of them is a credential.
func TestResumePromptKeepsOrdinaryIdentitiesThroughTheScrub(t *testing.T) {
	events := []Event{
		{Sequence: 1, Type: EventMessage, Payload: toolContextPayload(t, map[string]any{"role": "user", "content": "go"})},
		{Sequence: 2, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c1", "name": "read_file", "arguments": `{"path":"internal/sessions/exec_session.go","offset":1,"limit":40}`})},
		{Sequence: 3, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{"id": "c2", "name": "grep", "arguments": `{"pattern":"func toolCallIdentity","glob":"src/**/*.tsx"}`})},
		{Sequence: 4, Type: EventError, Payload: toolContextPayload(t, map[string]any{"message": "provider error"})},
	}
	out := resumePrompt(t, events)
	for _, kept := range []string{"internal/sessions/exec_session.go", "func toolCallIdentity", "src/**/*.tsx"} {
		if !strings.Contains(out, kept) {
			t.Errorf("identity %q was lost to the scrub:\n%s", kept, out)
		}
	}
	// Numbers are not strings and are nothing to scrub; they must survive whole.
	if !strings.Contains(out, "40") {
		t.Errorf("a numeric read window did not survive the scrub:\n%s", out)
	}
}

// AND A FIELD THIS FILE HAS NEVER HEARD OF DOES NOT RIDE ALONG.
//
// toolResultOutcome builds its output from the fields it keeps, so an unknown
// one cannot reach a prompt through it. The call side edited arguments in place
// and returned every sibling key, which is the opposite contract: a producer
// recording one more top-level field, or the same payload with no arguments at
// all, put that field straight into the next turn. The projection names what
// survives, so both halves now drop what they do not name.
func TestResumePromptProjectsTopLevelToolCallFields(t *testing.T) {
	const body = "opaque top-level payload that must not be replayed"
	events := []Event{
		{Sequence: 1, Type: EventMessage, Payload: toolContextPayload(t, map[string]any{"role": "user", "content": "go"})},
		{Sequence: 2, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{
			"id": "c1", "name": "write_file", "arguments": `{"path":"deploy/prod.env"}`, "rawInput": body,
		})},
		// The same shape with no arguments at all, which used to return the whole
		// payload untouched.
		{Sequence: 3, Type: EventToolCall, Payload: toolContextPayload(t, map[string]any{
			"id": "c2", "name": "read_file", "stashedContent": body,
		})},
		{Sequence: 4, Type: EventError, Payload: toolContextPayload(t, map[string]any{"message": "provider error"})},
	}
	out := resumePrompt(t, events)

	for _, leaked := range []string{body, "rawInput", "stashedContent"} {
		if strings.Contains(out, leaked) {
			t.Errorf("an unnamed top-level field %q reached the resume prompt:\n%s", leaked, out)
		}
	}
	// The identity itself still survives, from both calls.
	for _, kept := range []string{"c1", "write_file", "deploy/prod.env", "c2", "read_file"} {
		if !strings.Contains(out, kept) {
			t.Errorf("call identity %q was lost to the projection:\n%s", kept, out)
		}
	}
}
