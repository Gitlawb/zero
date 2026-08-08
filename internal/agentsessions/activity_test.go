package agentsessions

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
)

func summaryTexts(t *testing.T, events []sessions.AppendEventInput) []string {
	t.Helper()
	out := []string{}
	for _, event := range events {
		if event.Type != sessions.EventCompaction {
			continue
		}
		out = append(out, str(t, event, "summary"))
	}
	return out
}

func joinedSummary(t *testing.T, events []sessions.AppendEventInput) string {
	t.Helper()
	return strings.Join(summaryTexts(t, events), "\n")
}

// claudeToolLines builds a transcript with one tool_use/tool_result pair.
func claudeToolLines(id, name, arguments, output string, failed bool) []string {
	isError := "false"
	if failed {
		isError = "true"
	}
	return []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id +
			`","name":"` + name + `","input":` + arguments + `}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id +
			`","is_error":` + isError + `,"content":"` + output + `"}]}}`,
	}
}

func TestTheSummaryNamesFilesCommandsAndSearches(t *testing.T) {
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	lines = append(lines, claudeToolLines("t1", "Read", `{"file_path":"/w/parser.go"}`, "package main", false)...)
	lines = append(lines, claudeToolLines("t2", "Edit", `{"file_path":"/w/lexer.go"}`, "ok", false)...)
	lines = append(lines, claudeToolLines("t3", "Bash", `{"command":"go test ./..."}`, "PASS", false)...)
	lines = append(lines, claudeToolLines("t4", "Grep", `{"pattern":"handleResume"}`, "3 hits", false)...)

	events, err := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	summary := joinedSummary(t, events)

	for _, want := range []string{
		"4 tool calls",
		"Files read: parser.go",   // relative to cwd, not the absolute path
		"Files changed: lexer.go", // Edit is classified as mutating
		"Commands run: go test ./...",
		"Searched for: handleResume",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
}

// TestAFailedCallDoesNotClaimItReadTheFile is the correctness fix the real
// corpus prompted: a Read of a path that does not exist was still listed under
// "Files read", which the next model would take as fact.
func TestAFailedCallDoesNotClaimItReadTheFile(t *testing.T) {
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	lines = append(lines, claudeToolLines("t1", "Read", `{"file_path":"/home/wrong/parser.go"}`,
		"File does not exist.", true)...)
	lines = append(lines, claudeToolLines("t2", "Read", `{"file_path":"/w/parser.go"}`, "package main", false)...)

	events, err := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	summary := joinedSummary(t, events)

	if strings.Contains(summary, "/home/wrong/parser.go") && strings.Contains(summary, "Files read") {
		for _, line := range summaryTexts(t, events) {
			if strings.HasPrefix(line, "Files read") && strings.Contains(line, "/home/wrong/parser.go") {
				t.Errorf("a path whose read FAILED is listed as read:\n%s", line)
			}
		}
	}
	if !strings.Contains(summary, "Files read: parser.go") {
		t.Errorf("the successful read was lost:\n%s", summary)
	}
	// The failure itself must still be reported — that is the most useful line.
	if !strings.Contains(summary, "Failures") || !strings.Contains(summary, "File does not exist") {
		t.Errorf("the failure was not reported:\n%s", summary)
	}
}

// TestEverySummaryEventSurvivesTheDigestIntact is the constraint that decided
// the shape of this feature. sessions.summarizePayload truncates each event at
// 500 chars, so one combined summary would lose its tail; each event must fit.
func TestEverySummaryEventSurvivesTheDigestIntact(t *testing.T) {
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	for i := 0; i < 40; i++ {
		lines = append(lines, claudeToolLines(
			"t"+itoa(i), "Read",
			`{"file_path":"/w/a/very/long/directory/name/that/eats/budget/file`+itoa(i)+`.go"}`,
			"ok", false)...)
	}
	events, err := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	summaries := summaryTexts(t, events)
	if len(summaries) == 0 {
		t.Fatal("no summary events produced")
	}
	for _, summary := range summaries {
		if length := len([]rune(summary)); length > maxSummaryEventChars {
			t.Errorf("summary event is %d chars, over the %d budget — it will be cut "+
				"mid-sentence by the resume digest:\n%s", length, maxSummaryEventChars, summary)
		}
	}
	// And the overflow must be stated, not silently dropped.
	if !strings.Contains(strings.Join(summaries, "\n"), "more)") {
		t.Errorf("40 files collapsed to a short list with no overflow note:\n%s",
			strings.Join(summaries, "\n"))
	}
}

// TestTheSummaryReachesTheModel is the whole point: these events must survive
// sessions.promptContextEvents, the filter that drops tool events.
func TestTheSummaryReachesTheModel(t *testing.T) {
	home := t.TempDir()
	lines := []string{`{"type":"user","cwd":"/w","sessionId":"s1","message":{"role":"user","content":"fix the parser"}}`}
	lines = append(lines, claudeToolLines("t1", "Read", `{"file_path":"/w/parser.go"}`, "package main", false)...)
	lines = append(lines, claudeToolLines("t2", "Bash", `{"command":"go test ./parser"}`, "FAIL", true)...)
	writeFile(t, filepath.Join(home, ".claude", "projects", "-w", "s1.jsonl"),
		strings.Join(lines, "\n")+"\n")

	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(t.TempDir(), "sessions")})
	result, err := Import(store, ClaudeCode(testEnv(home, nil)), "s1", ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := sessions.PrepareExec(sessions.PrepareExecOptions{Store: store, Resume: result.Session.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	digest := sessions.FormatExecPrompt("what is left?", prepared)

	// Before this feature the digest held only the two prose messages. These
	// facts existed solely in tool events, which never reach the model.
	for _, want := range []string{
		"tool calls",
		"parser.go",
		"go test ./parser",
		"Failures",
	} {
		if !strings.Contains(digest, want) {
			t.Errorf("the resume digest is missing %q — the summary is not reaching "+
				"the model:\n%s", want, digest)
		}
	}
}

func TestASessionWithNoToolCallsGetsNoSummary(t *testing.T) {
	events, err := translateFamily1(writeTranscript(t,
		`{"type":"user","cwd":"/w","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	), ReadOptions{Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryTexts(t, events); len(got) != 0 {
		t.Errorf("got %d summary events for a conversation with no tools, want none: %v", len(got), got)
	}
}

// TestAnUnknownToolSchemaDegradesToACount covers the case this design must not
// get wrong: an agent whose arguments use names we have never seen. Naming the
// tool and its count is true; guessing which field held a filename would not be.
func TestAnUnknownToolSchemaDegradesToACount(t *testing.T) {
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	lines = append(lines, claudeToolLines("t1", "MysteryTool", `{"wibble":"/w/secret.go","flim":3}`, "ok", false)...)
	lines = append(lines, claudeToolLines("t2", "MysteryTool", `{"wibble":"/w/other.go"}`, "ok", false)...)

	events, _ := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	summary := joinedSummary(t, events)

	if !strings.Contains(summary, "MysteryTool x2") {
		t.Errorf("an unrecognised schema should still be counted:\n%s", summary)
	}
	for _, guessed := range []string{"secret.go", "other.go", "Files read"} {
		if strings.Contains(summary, guessed) {
			t.Errorf("summary guessed %q out of an unknown argument schema:\n%s", guessed, summary)
		}
	}
}

func TestRepeatedWorkIsNotListedRepeatedly(t *testing.T) {
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	for i := 0; i < 8; i++ {
		lines = append(lines, claudeToolLines("t"+itoa(i), "Read", `{"file_path":"/w/same.go"}`, "ok", false)...)
	}
	events, _ := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	summary := joinedSummary(t, events)

	if count := strings.Count(summary, "same.go"); count != 1 {
		t.Errorf("same.go appears %d times, want 1 — re-reads must dedupe:\n%s", count, summary)
	}
	// The call count is still the true one.
	if !strings.Contains(summary, "8 tool calls") {
		t.Errorf("dedupe must not change the call count:\n%s", summary)
	}
}

// TestSecretsInToolArgumentsAreRedacted: the summary is built from another
// program's logs, so it goes through the same chokepoint as every other
// imported string.
func TestSecretsInToolArgumentsAreRedacted(t *testing.T) {
	const leaked = "sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKKKLLLL"
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	lines = append(lines, claudeToolLines("t1", "Bash", `{"command":"export K=`+leaked+`"}`, "ok", false)...)

	events, _ := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), leaked) {
		t.Errorf("a secret from a tool argument survived into the summary:\n%s", encoded)
	}
}

func TestPathsOutsideTheWorkspaceKeepTheirAbsoluteForm(t *testing.T) {
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	lines = append(lines, claudeToolLines("t1", "Read", `{"file_path":"/w/inside.go"}`, "ok", false)...)
	lines = append(lines, claudeToolLines("t2", "Read", `{"file_path":"/elsewhere/outside.go"}`, "ok", false)...)

	events, _ := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	summary := joinedSummary(t, events)

	if !strings.Contains(summary, "inside.go") || strings.Contains(summary, "/w/inside.go") {
		t.Errorf("a file in the workspace should be shown relative:\n%s", summary)
	}
	if !strings.Contains(summary, "/elsewhere/outside.go") {
		t.Errorf("a file outside the workspace should keep its absolute path — that "+
			"it is elsewhere is the interesting part:\n%s", summary)
	}
}

func TestSummaryEventsComeLastSoTheySitNearestTheNewRequest(t *testing.T) {
	lines := []string{`{"type":"user","cwd":"/w","message":{"role":"user","content":"go"}}`}
	lines = append(lines, claudeToolLines("t1", "Read", `{"file_path":"/w/a.go"}`, "ok", false)...)

	events, _ := translateFamily1(writeTranscript(t, lines...), ReadOptions{Cwd: "/w"})
	if len(events) < 2 {
		t.Fatal("expected conversation events plus a summary")
	}
	if events[len(events)-1].Type != sessions.EventCompaction {
		t.Errorf("last event is %s, want the summary last so it survives the "+
			"80-event window and reads as a footer", events[len(events)-1].Type)
	}
	// And the conversation itself still comes first.
	if events[0].Type != sessions.EventMessage {
		t.Errorf("first event is %s, want the original conversation to lead", events[0].Type)
	}
}
