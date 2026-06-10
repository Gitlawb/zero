package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

	// Answer the first question.
	next.input.SetValue("React")
	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
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

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if len(next.suggestions) != 0 || next.suggestionsAreFiles {
		t.Fatalf("ask_user resolve should keep suggestions clear, got suggestions=%#v files=%v", next.suggestions, next.suggestionsAreFiles)
	}
}
