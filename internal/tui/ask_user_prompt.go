package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
)

// ask_user_prompt.go holds the interactive selector for agent-asked questions,
// mirroring permission_prompt.go. When the current question carries Options the
// prompt renders a picker (the options plus a trailing "type my own" entry); the
// cursor starts on the recommended option. Selecting "type my own" — or a question
// with no options — falls back to the existing free-text composer flow, so there is
// no regression for open-ended questions.

// askUserTypeMyOwnLabel is the trailing selector entry that drops into free-text.
const askUserTypeMyOwnLabel = "✎ Type my own answer…"

// askUserSelectableCount is the number of cursor positions for a question: every
// option plus the "type my own" entry. Zero when the question has no options (the
// prompt is plain free-text and the selector is not shown).
func askUserSelectableCount(question agent.AskUserQuestion) int {
	if len(question.Options) == 0 {
		return 0
	}
	return len(question.Options) + 1
}

// recommendedAskUserIndex is the cursor position the selector rests on by default:
// the recommended option when it matches one (the parser guarantees Recommended is
// either empty or a member of Options), otherwise the first option.
func recommendedAskUserIndex(question agent.AskUserQuestion) int {
	if question.Recommended == "" {
		return 0
	}
	for index, option := range question.Options {
		if option == question.Recommended {
			return index
		}
	}
	return 0
}

// clampAskUserCursor keeps a cursor index within [0, n).
func clampAskUserCursor(cursor, n int) int {
	if n <= 0 {
		return 0
	}
	if cursor < 0 {
		return 0
	}
	if cursor >= n {
		return n - 1
	}
	return cursor
}

// askUserCurrentQuestion returns the question the prompt is currently on, and
// whether the index is valid.
func (p *pendingAskUserPrompt) askUserCurrentQuestion() (agent.AskUserQuestion, bool) {
	if p == nil || p.index < 0 || p.index >= len(p.request.Questions) {
		return agent.AskUserQuestion{}, false
	}
	return p.request.Questions[p.index], true
}

// syncQuestionState initialises the selector/free-text state for the question the
// prompt is now on. Called when the prompt is created and after each answer
// advances the index. Only a SINGLE-select question with options enters the picker
// (resting on the recommended choice). A multi-select question — which a single-pick
// selector cannot represent — and an open-ended question both use free-text, so a
// multi-select answer is never silently narrowed to one option.
func (p *pendingAskUserPrompt) syncQuestionState() {
	question, ok := p.askUserCurrentQuestion()
	if !ok || len(question.Options) == 0 || question.MultiSelect {
		p.typing = true
		p.cursor = 0
		return
	}
	p.typing = false
	p.cursor = recommendedAskUserIndex(question)
}

// moveAskUserCursor advances the highlighted selector entry by delta, wrapping at
// the ends. A no-op in free-text mode or when no prompt is pending. The cursor lives
// on the pending prompt pointer, matching movePermissionCursor.
func (m model) moveAskUserCursor(delta int) model {
	pending := m.pendingAskUser
	if pending == nil || pending.typing {
		return m
	}
	question, ok := pending.askUserCurrentQuestion()
	if !ok {
		return m
	}
	n := askUserSelectableCount(question)
	if n <= 0 {
		return m
	}
	cursor := (clampAskUserCursor(pending.cursor, n) + delta) % n
	if cursor < 0 {
		cursor += n
	}
	pending.cursor = cursor
	return m
}

// confirmAskUser resolves the Enter key for an ask-user prompt. In free-text mode it
// submits the composer value (the legacy path); in selector mode it either records
// the highlighted option as the answer, or — when "type my own" is highlighted —
// switches this question into free-text mode so the user can type freely.
func (m model) confirmAskUser() (tea.Model, tea.Cmd) {
	pending := m.pendingAskUser
	if pending == nil {
		return m, nil
	}
	question, ok := pending.askUserCurrentQuestion()
	if !ok {
		return m.resolveAskUser(false)
	}
	if pending.typing || len(question.Options) == 0 {
		return m.advanceAskUser(strings.TrimSpace(m.input.Value()))
	}
	cursor := clampAskUserCursor(pending.cursor, askUserSelectableCount(question))
	if cursor >= len(question.Options) {
		// "type my own" — drop into the free-text composer for this question.
		pending.typing = true
		m.input.SetValue("")
		return m, nil
	}
	return m.advanceAskUser(question.Options[cursor])
}

// escapeAskUser handles Esc for an ask-user prompt. From the "type my own"
// free-text state of a question that HAS options, it steps back to the selector
// (keeping the cursor on the "type my own" entry) rather than cancelling — so Esc is
// an undo of the free-text drop-in. In every other case (selector mode, or an
// open-ended question with no options) it cancels the questionnaire as before.
func (m model) escapeAskUser() (tea.Model, tea.Cmd) {
	pending := m.pendingAskUser
	if pending == nil {
		return m, nil
	}
	if question, ok := pending.askUserCurrentQuestion(); ok && pending.typing && !question.MultiSelect && len(question.Options) > 0 {
		pending.typing = false
		pending.cursor = clampAskUserCursor(pending.cursor, askUserSelectableCount(question))
		m.input.SetValue("")
		return m, nil
	}
	return m.resolveAskUser(true)
}

// advanceAskUser records answer for the current question and moves to the next,
// resolving the whole questionnaire once the last question is answered. It is the
// single advance path shared by the free-text submit and the option select, so both
// feed the same answer channel the agent loop waits on.
func (m model) advanceAskUser(answer string) (tea.Model, tea.Cmd) {
	pending := m.pendingAskUser
	if pending == nil {
		return m, nil
	}
	pending.answers = append(pending.answers, answer)
	pending.index++
	m.input.SetValue("")
	if pending.index >= len(pending.request.Questions) {
		return m.resolveAskUser(false)
	}
	pending.syncQuestionState() // selector vs free-text for the next question
	return m, nil
}
