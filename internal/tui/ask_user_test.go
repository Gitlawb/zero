package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
)

func testAskUserRequest() agent.AskUserRequest {
	return agent.AskUserRequest{
		ToolCallID: "call_1",
		Header:     "Need a couple of details",
		Questions: []agent.AskUserQuestion{
			{Question: "Which framework?", Options: []string{"React", "Vue"}},
			{Question: "TypeScript?"},
		},
	}
}

func TestAskUserRequestShowsFocusedPrompt(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, cmd := m.Update(askUserRequestMsg{
		runID:   7,
		request: testAskUserRequest(),
	})
	next := updated.(model)

	_ = cmd // the settle pass may emit a benign scrollback flush command
	if next.pendingAskUser == nil {
		t.Fatalf("expected ask_user prompt to be pending, got %#v", next)
	}
	if countTranscriptRows(next.transcript, rowAskUser) != 1 {
		t.Fatalf("expected one ask_user transcript row, got %#v", next.transcript)
	}
	view := next.View()
	for _, want := range []string{"Which framework?", "React", "Vue", "question 1 of 2"} {
		assertContains(t, view, want)
	}
}

func TestAskUserPromptCollectsAnswersInOrder(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: testAskUserRequest(),
		answer: func(values []string) {
			answers = append(answers, values)
		},
	})
	next := updated.(model)

	// Q1 has options -> selector mode; Enter selects the default (first) option "React"
	// without any composer input.
	updated, cmd := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected first answer to advance synchronously")
	}
	if next.pendingAskUser == nil {
		t.Fatal("expected prompt to remain pending after first answer")
	}
	if len(answers) != 0 {
		t.Fatalf("expected no answers delivered until all questions answered, got %#v", answers)
	}

	// Answer the second (final) question.
	next.input.SetValue("yes")
	updated, cmd = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected final answer to resolve synchronously")
	}
	if next.pendingAskUser != nil {
		t.Fatalf("expected prompt to clear after final answer, got %#v", next.pendingAskUser)
	}
	if len(answers) != 1 {
		t.Fatalf("expected one delivery of answers, got %#v", answers)
	}
	if len(answers[0]) != 2 || answers[0][0] != "React" || answers[0][1] != "yes" {
		t.Fatalf("expected answers [React yes], got %#v", answers[0])
	}
}

func askUserRecommendedRequest() agent.AskUserRequest {
	return agent.AskUserRequest{
		ToolCallID: "call_db",
		Questions: []agent.AskUserQuestion{
			{Question: "Which database?", Options: []string{"Postgres", "SQLite", "MySQL"}, Recommended: "SQLite"},
		},
	}
}

// A question with options + a recommended choice renders the selector, rests the
// cursor on the recommended option, and Enter selects it without typing.
func TestAskUserSelectorDefaultsToRecommended(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: askUserRecommendedRequest(),
		answer:  func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)

	if next.pendingAskUser == nil || next.pendingAskUser.typing {
		t.Fatalf("expected selector mode for a question with options, got %#v", next.pendingAskUser)
	}
	if next.pendingAskUser.cursor != 1 {
		t.Fatalf("expected cursor to rest on the recommended option (index 1), got %d", next.pendingAskUser.cursor)
	}
	for _, want := range []string{"Postgres", "SQLite", "MySQL", "(recommended)", askUserTypeMyOwnLabel} {
		assertContains(t, next.View(), want)
	}

	// Enter selects the recommended default — no composer text involved.
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.pendingAskUser != nil {
		t.Fatalf("expected single-question prompt to clear after selecting, got %#v", next.pendingAskUser)
	}
	if len(answers) != 1 || len(answers[0]) != 1 || answers[0][0] != "SQLite" {
		t.Fatalf("expected selecting the recommended default to return [SQLite], got %#v", answers)
	}
}

// Arrowing to the trailing "type my own" entry switches the question into free-text
// mode; the typed answer is what gets returned.
func TestAskUserSelectorTypeMyOwnReturnsTypedText(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: askUserRecommendedRequest(),
		answer:  func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)

	// Cursor starts at index 1 (SQLite); move down twice to the "type my own" entry
	// (options=3 -> type-my-own index 3).
	for i := 0; i < 2; i++ {
		updated, _ = next.Update(testKey(tea.KeyDown))
		next = updated.(model)
	}
	if next.pendingAskUser.cursor != 3 {
		t.Fatalf("expected cursor on the 'type my own' entry (index 3), got %d", next.pendingAskUser.cursor)
	}

	// Enter on "type my own" switches to free-text and delivers nothing yet.
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.pendingAskUser == nil || !next.pendingAskUser.typing {
		t.Fatalf("expected 'type my own' to switch into free-text typing mode, got %#v", next.pendingAskUser)
	}
	if len(answers) != 0 {
		t.Fatalf("expected no answer delivered when switching to free-text, got %#v", answers)
	}
	assertContains(t, next.View(), "type your own answer")

	// The typed answer must appear ONLY in the composer, never echoed inside the ask
	// card (the double-display bug): the focused card carries no input.
	next.input.SetValue("CockroachDB")
	if card := renderFocusedAskUserPrompt(*next.pendingAskUser, next.width); strings.Contains(card, "CockroachDB") {
		t.Fatalf("ask card must not echo the typed answer; card=\n%s", card)
	}

	// Submit the typed answer.
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "CockroachDB" {
		t.Fatalf("expected typed free-text answer [CockroachDB], got %#v", answers)
	}
}

// Esc from the "type my own" free-text steps back to the selector for that question
// — it must NOT cancel the questionnaire or deliver answers; the user can then pick
// a suggested option instead.
func TestAskUserTypeMyOwnEscReturnsToSelector(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: askUserRecommendedRequest(),
		answer:  func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)

	// Into "type my own" (index 3) and free-text.
	for i := 0; i < 2; i++ {
		updated, _ = next.Update(testKey(tea.KeyDown))
		next = updated.(model)
	}
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.pendingAskUser == nil || !next.pendingAskUser.typing {
		t.Fatal("expected free-text mode after choosing type-my-own")
	}
	next.input.SetValue("scratch")

	// Esc returns to the selector, does not cancel, does not deliver.
	updated, _ = next.Update(testKey(tea.KeyEsc))
	next = updated.(model)
	if next.pendingAskUser == nil {
		t.Fatal("Esc from type-my-own must NOT cancel the questionnaire")
	}
	if next.pendingAskUser.typing {
		t.Fatal("Esc from type-my-own must return to the selector (typing=false)")
	}
	if len(answers) != 0 {
		t.Fatalf("Esc back to the selector must not deliver answers, got %#v", answers)
	}
	if !next.pending {
		t.Fatal("the run must keep running after Esc steps back")
	}

	// From the selector, move up to a real option and select it.
	updated, _ = next.Update(testKey(tea.KeyUp)) // 3 -> 2 (MySQL)
	next = updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "MySQL" {
		t.Fatalf("expected selecting MySQL after returning to the selector, got %#v", answers)
	}
}

// A question with NO options keeps the plain free-text prompt exactly as before (no
// selector, no regression).
func TestAskUserNoOptionsIsPlainFreeText(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, _ := m.Update(askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			ToolCallID: "call_open",
			Questions:  []agent.AskUserQuestion{{Question: "Describe the desired behavior"}},
		},
		answer: func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)

	if next.pendingAskUser == nil || !next.pendingAskUser.typing {
		t.Fatalf("expected free-text (typing) mode for a question with no options, got %#v", next.pendingAskUser)
	}
	view := next.View()
	assertContains(t, view, "type an answer")
	if transcriptContains(next.transcript, askUserTypeMyOwnLabel) {
		t.Fatalf("a no-options question must not show the selector's type-my-own entry")
	}

	next.input.SetValue("my free-form answer")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "my free-form answer" {
		t.Fatalf("expected free-text answer, got %#v", answers)
	}
}

// A multi-select question can't be a single-pick selector, so it renders as
// free-text (surfacing the options as suggestions) — the typed answer is returned
// verbatim and nothing is silently narrowed to one option.
func TestAskUserMultiSelectFallsBackToFreeText(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, _ := m.Update(askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			ToolCallID: "call_multi",
			Questions: []agent.AskUserQuestion{
				{Question: "Which checks?", Options: []string{"lint", "test", "typecheck"}, MultiSelect: true},
			},
		},
		answer: func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)

	if next.pendingAskUser == nil || !next.pendingAskUser.typing {
		t.Fatalf("expected multi-select to use free-text, got %#v", next.pendingAskUser)
	}
	// Options are surfaced as suggestions, not a single-pick list.
	assertContains(t, next.View(), "suggested:")
	assertContains(t, next.View(), "lint")

	next.input.SetValue("lint, typecheck")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "lint, typecheck" {
		t.Fatalf("expected the typed multi-answer returned verbatim, got %#v", answers)
	}
}

// Typing a printable key while the single-select selector is showing flips straight
// into free-text (seeding the keystroke) rather than being swallowed and discarded.
func TestAskUserTypingInSelectorSwitchesToFreeText(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: askUserRecommendedRequest(), // single-select, 3 options
		answer:  func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)
	if next.pendingAskUser.typing {
		t.Fatal("expected selector mode initially")
	}

	// Type a character: it must switch to free-text AND be captured (not swallowed).
	updated, _ = next.Update(testKeyText("M"))
	next = updated.(model)
	if !next.pendingAskUser.typing {
		t.Fatal("expected a printable keystroke to switch the selector into free-text")
	}
	if next.input.Value() != "M" {
		t.Fatalf("expected the keystroke to be captured in the composer, got %q", next.input.Value())
	}

	// Finish typing and submit; the typed answer wins (not a discarded option).
	next.input.SetValue("MariaDB")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "MariaDB" {
		t.Fatalf("expected the typed answer MariaDB, got %#v", answers)
	}
}

func TestAskUserPromptEscDeliversCollectedAnswers(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: testAskUserRequest(),
		answer: func(values []string) {
			answers = append(answers, values)
		},
	})
	next := updated.(model)

	// Esc while an ask_user prompt is active cancels the questionnaire and must
	// still deliver a (partial/empty) answer set so the run never deadlocks.
	updated, _ = next.Update(testKey(tea.KeyEsc))
	next = updated.(model)

	if next.pendingAskUser != nil {
		t.Fatalf("expected ask_user prompt to clear after Esc, got %#v", next.pendingAskUser)
	}
	if len(answers) != 1 {
		t.Fatalf("expected the answer callback to fire on Esc, got %#v", answers)
	}
	// Esc on an ask_user prompt cancels only the questionnaire, not the run, so the
	// run stays pending and continues with the degraded (empty) answers.
	if !next.pending {
		t.Fatal("expected the run to keep running after Esc cancels only the ask_user prompt")
	}
}

func TestAskUserPromptBlocksNormalSubmit(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: testAskUserRequest(),
		answer:  func([]string) {},
	})
	next := updated.(model)
	next.input.SetValue("/help")

	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)

	if transcriptContains(next.transcript, "Available commands") {
		t.Fatalf("ask_user prompt should capture Enter, not run commands: %#v", next.transcript)
	}
	if next.pendingAskUser == nil {
		t.Fatal("expected ask_user prompt to remain pending after answering one question")
	}
}

func TestAskUserRequestClearsComposerDraft(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m = typeRunes(t, m, "hidden followup")
	if !m.composerActive || m.composerValue() == "" {
		t.Fatalf("test setup expected active composer draft, got active=%v value=%q", m.composerActive, m.composerValue())
	}

	updated, _ := m.Update(askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			ToolCallID: "call_1",
			Questions:  []agent.AskUserQuestion{{Question: "Proceed?"}},
		},
		answer: func(values []string) {
			answers = append(answers, values)
		},
	})
	next := updated.(model)

	if next.composerActive || next.composerValue() != "" {
		t.Fatalf("ask_user should clear composer draft, active=%v value=%q", next.composerActive, next.composerValue())
	}
	next.input.SetValue("yes")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "yes" {
		t.Fatalf("expected answer to use ask_user input only, got %#v", answers)
	}
	if transcriptContains(next.transcript, "hidden followup") {
		t.Fatalf("hidden composer draft leaked into transcript: %#v", next.transcript)
	}
}

func TestAskUserRequestClearsStaleSuggestions(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.suggestions = []commandSuggestion{{Name: "/model", Desc: "Pick a model."}}
	m.suggestionIdx = 0
	m.suggestionsAreFiles = true

	updated, _ := m.Update(askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			ToolCallID: "call_1",
			Questions:  []agent.AskUserQuestion{{Question: "Proceed?"}},
		},
		answer: func([]string) {},
	})
	next := updated.(model)

	if len(next.suggestions) != 0 || next.suggestionsAreFiles {
		t.Fatalf("ask_user should clear stale suggestions, got suggestions=%#v files=%v", next.suggestions, next.suggestionsAreFiles)
	}
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(next.suggestions) != 0 || next.suggestionsAreFiles {
		t.Fatalf("ask_user resolve should keep suggestions clear, got suggestions=%#v files=%v", next.suggestions, next.suggestionsAreFiles)
	}
}
