package agentsessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCodexStore(t *testing.T, lines ...string) (Env, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "sessions", "2026", "08", "01",
		"rollout-2026-08-01T10-00-00-019f73d7-e215-7ce0-ab38-d9e6db354717.jsonl")
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return testEnv(home, nil), path
}

// TestATurnContextDoesNotBreakOnAFieldItSharesByNameOnly is a regression test
// for a defect the real corpus exposed.
//
// "summary" is an array of blocks on a reasoning item and the bare string "auto"
// on turn_context. Typing it as []codexBlock made encoding/json reject the whole
// turn_context record, so the model id vanished from every Codex session — with
// no error at any layer, because a record that fails to decode is simply skipped.
//
// Nothing about the model id itself is special here; the point is that ONE
// mistyped field in a union struct silently discards every other field on that
// record.
func TestATurnContextDoesNotBreakOnAFieldItSharesByNameOnly(t *testing.T) {
	env, _ := writeCodexStore(t,
		`{"type":"session_meta","timestamp":"2026-08-01T10:00:00.000Z","payload":{"session_id":"019f73d7-e215-7ce0-ab38-d9e6db354717","cwd":"/Users/someone/proj"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5.6-sol","effort":"high","summary":"auto"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser"}]}}`,
	)

	found, err := Codex(env).Discover("")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("got %d sessions, want 1", len(found))
	}
	if found[0].ModelID != "gpt-5.6-sol" {
		t.Errorf("ModelID = %q, want gpt-5.6-sol — a string-valued \"summary\" on "+
			"turn_context must not discard the rest of the record", found[0].ModelID)
	}
	if found[0].Cwd != "/Users/someone/proj" {
		t.Errorf("Cwd = %q", found[0].Cwd)
	}
	if found[0].ID != "019f73d7-e215-7ce0-ab38-d9e6db354717" {
		t.Errorf("ID = %q, want the uuid from the filename tail", found[0].ID)
	}
}

// TestCodexHarnessChatterIsNotTheConversation covers the two ways Codex speaks
// to itself through channels that look like conversation: "developer" messages
// carrying its system prompt, and <environment_context> injected as a user turn.
// Neither is the user's work, and both would otherwise dominate a title and the
// imported transcript.
func TestCodexHarnessChatterIsNotTheConversation(t *testing.T) {
	env, path := writeCodexStore(t,
		`{"type":"session_meta","timestamp":"2026-08-01T10:00:00.000Z","payload":{"session_id":"s","cwd":"/w"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are Codex. Follow the permissions policy."}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/w</cwd>\n</environment_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"install hermes on this pc"}]}}`,
	)

	found, _ := Codex(env).Discover("")
	if len(found) != 1 {
		t.Fatalf("got %d sessions, want 1", len(found))
	}
	if found[0].Title != "install hermes on this pc" {
		t.Errorf("Title = %q, want the first real human turn", found[0].Title)
	}

	events, err := translateCodex(path, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want only the human turn: %+v", len(events), events)
	}
	encoded, _ := json.Marshal(events)
	for _, unwanted := range []string{"You are Codex", "environment_context"} {
		if strings.Contains(string(encoded), unwanted) {
			t.Errorf("harness chatter %q was imported as conversation", unwanted)
		}
	}
}

func TestCodexToolCallsPairUpAcrossBothCallShapes(t *testing.T) {
	_, path := writeCodexStore(t,
		`{"type":"session_meta","timestamp":"2026-08-01T10:00:00.000Z","payload":{"session_id":"s","cwd":"/w"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"wait","call_id":"call_1","arguments":"{\"ms\":10}"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"done"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call_2","input":"ls -la"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_2","output":"[{\"type\":\"input_text\",\"text\":\"a.go\"}]"}}`,
	)
	all, err := translateCodex(path, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events := conversationEvents(all)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}
	// Both call shapes must carry their name through to their result.
	for _, pair := range [][2]int{{0, 1}, {2, 3}} {
		call, result := events[pair[0]], events[pair[1]]
		if str(t, call, "toolCallId") != str(t, result, "toolCallId") {
			t.Errorf("call/result ids differ: %q vs %q", str(t, call, "toolCallId"), str(t, result, "toolCallId"))
		}
		if str(t, call, "name") != str(t, result, "name") {
			t.Errorf("result name %q does not match its call %q", str(t, result, "name"), str(t, call, "name"))
		}
	}
	// An output stored as an escaped JSON array must render as its text, not as
	// raw JSON the reader has to decode by eye.
	if got := str(t, events[3], "output"); got != "a.go" {
		t.Errorf("escaped-array output = %q, want the flattened text", got)
	}
}

func TestCodexDiscoveryIsFixedDepth(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	good := filepath.Join(root, "2026", "08", "01", "rollout-a-019f73d7-e215-7ce0-ab38-d9e6db354717.jsonl")
	writeFile(t, good, `{"type":"session_meta","payload":{"session_id":"s","cwd":"/w"}}`+"\n")

	// A live OPENAI_API_KEY lives at ~/.codex/auth.json, one level above the
	// sessions root, plus decoy transcripts a walk would reach.
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `{"OPENAI_API_KEY":"sk-MUST_NEVER_BE_READ"}`)
	for _, stray := range []string{
		filepath.Join(root, "stray.jsonl"),
		filepath.Join(root, "2026", "deep.jsonl"),
		filepath.Join(root, "2026", "08", "01", "nested", "deeper.jsonl"),
		filepath.Join(home, ".codex", "leaked.jsonl"),
	} {
		writeFile(t, stray, `{"type":"session_meta","payload":{"session_id":"x","cwd":"/w"}}`+"\n")
	}

	paths := Codex(testEnv(home, nil)).(codex).transcripts()
	if len(paths) != 1 || paths[0] != good {
		t.Fatalf("transcripts = %v, want exactly [%s]", paths, good)
	}
}

func TestTheRealCodexCorpusStillParses(t *testing.T) {
	env := OSEnv()
	root := codexRoot(env)
	if root == "" {
		t.Skip("no home directory")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skip("no Codex store on this machine")
	}
	adapter := Codex(env)
	found, err := adapter.Discover("")
	if err != nil {
		t.Fatal(err)
	}
	total := len(adapter.(codex).transcripts())
	if total == 0 {
		t.Skip("store exists but holds no rollouts")
	}
	titled, modelled := 0, 0
	for _, session := range found {
		if session.Title != "" && session.Title != "untitled" && !isCodexContextInjection(session.Title) {
			titled++
		}
		if session.ModelID != "" {
			modelled++
		}
	}
	t.Logf("indexed %d of %d rollouts; %d titled, %d with a model", len(found), total, titled, modelled)
	if len(found) == 0 {
		t.Fatal("no Codex sessions indexed from a non-empty store")
	}
	// Both of these were zero before the fixes above; a regression takes them
	// back to zero rather than to some slightly-lower number.
	if titled == 0 {
		t.Error("no session got a real title — the context-injection filter has stopped working")
	}
	if modelled == 0 {
		t.Error("no session got a model — turn_context is being discarded again")
	}
}
