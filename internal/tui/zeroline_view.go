package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/zeroline"
)

// zerolineTickMsg advances the Zeroline animation frame (spinner) independently of
// agent events so "working…" spins smoothly between row updates.
type zerolineTickMsg time.Time

// bootFrames is how many ~120ms ticks the boot splash plays before the home
// page reveals (~1.9s), matching the mockup's splash timing.
const bootFrames = 16

func zerolineTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return zerolineTickMsg(t) })
}

// handleZerolineKeys intercepts theme controls when the zeroline skin is active.
// Digits 1-5 pick a color theme (only when the input is empty so they can still
// be typed); ctrl+t cycles; ctrl+l toggles light/dark.
func (m model) handleZerolineKeys(msg tea.KeyMsg) (model, bool) {
	switch msg.String() {
	case "ctrl+l":
		m.themeDark = !m.themeDark
		return m, true
	case "ctrl+t":
		m.themeVariant = (m.themeVariant + 1) % len(zeroline.Themes)
		return m, true
	}
	if strings.TrimSpace(m.input.Value()) == "" {
		if k := msg.String(); len(k) == 1 && k >= "1" && k <= "5" {
			m.themeVariant = int(k[0] - '1')
			return m, true
		}
	}
	return m, false
}

func (m model) zerolineView() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	// Boot splash reveals on launch, then the home page.
	if !m.booted && m.showSplash {
		return zeroline.RenderBoot(m.themeVariant, m.themeDark, m.frame, width, height)
	}

	header := zeroline.Header{
		Cwd:      m.cwd,
		Branch:   m.gitBranch,
		Model:    m.modelName,
		Provider: m.providerName,
	}

	// Home until the first turn is submitted.
	if m.showSplash {
		return zeroline.RenderHome(zeroline.HomeData{
			Variant:     m.themeVariant,
			Dark:        m.themeDark,
			Width:       width,
			Height:      height,
			Header:      header,
			Input:       m.input.View(),
			Suggestions: m.zerolineSuggestions(),
			SelectedIdx: m.suggestionIdx,
			Picker:      m.zerolinePicker(),
		})
	}

	rows := m.zerolineRows()
	running := false
	for _, r := range rows {
		if r.Kind == "toolcall" && r.Running {
			running = true
		}
	}
	askUser := m.zerolineAskUser()
	// A pending permission prompt or ask_user questionnaire is the focus: suppress
	// the working/thinking spinner so the gate/question shows instead.
	blocked := m.pendingPermission != nil || askUser != nil
	thinking := m.pending && m.streamingText == "" && !running && !blocked

	return zeroline.RenderChat(zeroline.ChatData{
		Variant:     m.themeVariant,
		Dark:        m.themeDark,
		Width:       width,
		Height:      height,
		Header:      header,
		Rows:        rows,
		Working:     m.pending && !blocked,
		Thinking:    thinking,
		Stream:      m.streamingText,
		TokS:        m.streamTokS(),
		Spin:        m.frame,
		Perm:        m.zerolinePerm(),
		AskUser:     askUser,
		Input:       m.input.View(),
		Suggestions: m.zerolineSuggestions(),
		SelectedIdx: m.suggestionIdx,
		Picker:      m.zerolinePicker(),
	})
}

// zerolineAskUser maps a pending ask_user questionnaire into zeroline render data,
// or nil when none is active. The focused question is the one at the prompt's
// current index.
func (m model) zerolineAskUser() *zeroline.AskUser {
	if m.pendingAskUser == nil {
		return nil
	}
	prompt := m.pendingAskUser
	total := len(prompt.request.Questions)
	index := prompt.index
	if index >= total {
		index = total - 1
	}
	if index < 0 {
		index = 0
	}
	out := &zeroline.AskUser{
		Header: prompt.request.Header,
		Index:  index,
		Total:  total,
		Input:  m.input.Value(),
	}
	if total > 0 {
		question := prompt.request.Questions[index]
		out.Question = question.Question
		out.Options = question.Options
	}
	return out
}

// zerolineSuggestions maps the live autocomplete matches into zeroline render
// data, or nil when the overlay is inactive (a modal is up or there are no
// matches).
func (m model) zerolineSuggestions() []zeroline.Suggestion {
	if !m.suggestionsActive() {
		return nil
	}
	out := make([]zeroline.Suggestion, 0, len(m.suggestions))
	for _, s := range m.suggestions {
		out = append(out, zeroline.Suggestion{Name: s.Name, Desc: s.Desc})
	}
	return out
}

// zerolinePicker maps an open selector into zeroline render data, or nil when no
// picker is open.
func (m model) zerolinePicker() *zeroline.Picker {
	if m.picker == nil {
		return nil
	}
	labels := make([]string, 0, len(m.picker.items))
	for _, item := range m.picker.items {
		labels = append(labels, item.Label)
	}
	return &zeroline.Picker{Title: m.picker.title, Items: labels, Selected: m.picker.selected}
}

// streamTokS estimates tokens/sec for the current streaming segment (~4 chars/token).
func (m model) streamTokS() int {
	if m.streamingText == "" {
		return 0
	}
	frames := m.frame - m.streamStartFrame
	if frames <= 0 {
		return 0
	}
	secs := float64(frames) * 0.12
	toks := float64(len([]rune(m.streamingText))) / 4.0
	if secs <= 0 {
		return 0
	}
	return int(toks / secs)
}

func (m model) zerolineRows() []zeroline.Row {
	// a tool call is "running" until a result row with the same id arrives
	resultIDs := make(map[string]bool)
	for _, r := range m.transcript {
		if r.kind == rowToolResult && r.id != "" {
			resultIDs[r.id] = true
		}
	}
	rows := make([]zeroline.Row, 0, len(m.transcript))
	for _, r := range m.transcript {
		switch r.kind {
		case rowUser:
			rows = append(rows, zeroline.Row{Kind: "user", Text: r.text})
		case rowAssistant:
			rows = append(rows, zeroline.Row{Kind: "assistant", Text: r.text})
		case rowToolCall:
			rows = append(rows, zeroline.Row{
				Kind:    "toolcall",
				Tool:    r.tool,
				Detail:  r.detail,
				Running: !(r.id != "" && resultIDs[r.id]),
			})
		case rowToolResult:
			rows = append(rows, zeroline.Row{Kind: "toolresult", Tool: r.tool, Status: string(r.status), Detail: r.detail})
		case rowPermission:
			rows = append(rows, zeroline.Row{Kind: "permission", Text: r.text})
		case rowAskUser:
			rows = append(rows, zeroline.Row{Kind: "system", Text: r.text, Detail: r.detail})
		case rowSystem:
			rows = append(rows, zeroline.Row{Kind: "system", Text: r.text})
		case rowError:
			rows = append(rows, zeroline.Row{Kind: "error", Text: r.text})
		}
	}
	return rows
}

func (m model) zerolinePerm() *zeroline.Perm {
	if m.pendingPermission == nil {
		return nil
	}
	req := m.pendingPermission.request
	return &zeroline.Perm{
		Tool:    req.ToolName,
		Risk:    string(req.Risk.Level),
		Reason:  req.Reason,
		Summary: req.SideEffect,
	}
}
