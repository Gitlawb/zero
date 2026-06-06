package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/zenline"
)

// zenlineTickMsg advances the Zenline animation frame (spinner) independently of
// agent events so "working…" spins smoothly between row updates.
type zenlineTickMsg time.Time

// bootFrames is how many ~120ms ticks the boot splash plays before the home
// page reveals (~1.9s), matching the mockup's splash timing.
const bootFrames = 16

func zenlineTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return zenlineTickMsg(t) })
}

// handleZenlineKeys intercepts theme controls when the zenline skin is active.
// Digits 1-5 pick a color theme (only when the input is empty so they can still
// be typed); ctrl+t cycles; ctrl+l toggles light/dark.
func (m model) handleZenlineKeys(msg tea.KeyMsg) (model, bool) {
	switch msg.String() {
	case "ctrl+l":
		m.themeDark = !m.themeDark
		return m, true
	case "ctrl+t":
		m.themeVariant = (m.themeVariant + 1) % len(zenline.Themes)
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

func (m model) zenlineView() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	// Boot splash reveals on launch, then the home page.
	if !m.booted && m.showSplash {
		return zenline.RenderBoot(m.themeVariant, m.themeDark, m.frame, width, height)
	}

	header := zenline.Header{
		Cwd:      m.cwd,
		Branch:   m.gitBranch,
		Model:    m.modelName,
		Provider: m.providerName,
	}

	// Home until the first turn is submitted.
	if m.showSplash {
		return zenline.RenderHome(zenline.HomeData{
			Variant: m.themeVariant,
			Dark:    m.themeDark,
			Width:   width,
			Height:  height,
			Header:  header,
			Input:   m.input.View(),
		})
	}

	rows := m.zenlineRows()
	running := false
	for _, r := range rows {
		if r.Kind == "toolcall" && r.Running {
			running = true
		}
	}
	thinking := m.pending && m.streamingText == "" && !running && m.pendingPermission == nil

	return zenline.RenderChat(zenline.ChatData{
		Variant:  m.themeVariant,
		Dark:     m.themeDark,
		Width:    width,
		Height:   height,
		Header:   header,
		Rows:     rows,
		Working:  m.pending && m.pendingPermission == nil,
		Thinking: thinking,
		Stream:   m.streamingText,
		TokS:     m.streamTokS(),
		Spin:     m.frame,
		Perm:     m.zenlinePerm(),
		Input:    m.input.View(),
	})
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

func (m model) zenlineRows() []zenline.Row {
	// a tool call is "running" until a result row with the same id arrives
	resultIDs := make(map[string]bool)
	for _, r := range m.transcript {
		if r.kind == rowToolResult && r.id != "" {
			resultIDs[r.id] = true
		}
	}
	rows := make([]zenline.Row, 0, len(m.transcript))
	for _, r := range m.transcript {
		switch r.kind {
		case rowUser:
			rows = append(rows, zenline.Row{Kind: "user", Text: r.text})
		case rowAssistant:
			rows = append(rows, zenline.Row{Kind: "assistant", Text: r.text})
		case rowToolCall:
			rows = append(rows, zenline.Row{
				Kind:    "toolcall",
				Tool:    r.tool,
				Detail:  r.detail,
				Running: !(r.id != "" && resultIDs[r.id]),
			})
		case rowToolResult:
			rows = append(rows, zenline.Row{Kind: "toolresult", Tool: r.tool, Status: string(r.status), Detail: r.detail})
		case rowPermission:
			rows = append(rows, zenline.Row{Kind: "permission", Text: r.text})
		case rowAskUser:
			rows = append(rows, zenline.Row{Kind: "system", Text: r.text, Detail: r.detail})
		case rowSystem:
			rows = append(rows, zenline.Row{Kind: "system", Text: r.text})
		case rowError:
			rows = append(rows, zenline.Row{Kind: "error", Text: r.text})
		}
	}
	return rows
}

func (m model) zenlinePerm() *zenline.Perm {
	if m.pendingPermission == nil {
		return nil
	}
	req := m.pendingPermission.request
	return &zenline.Perm{
		Tool:    req.ToolName,
		Risk:    string(req.Risk.Level),
		Reason:  req.Reason,
		Summary: req.SideEffect,
	}
}
