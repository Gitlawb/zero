package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
)

func (m model) modelAppliedNotice() string {
	return "Model: " + displayValue(m.modelName, "none") + " · " + displayValue(m.providerName, "default provider") + " · effort " + m.effortDisplay()
}

func (m model) effortAppliedNotice() string {
	return "Reasoning effort: " + m.effortDisplay()
}

func (m model) fastAppliedNotice() string {
	if m.activeServiceTier() == "priority" {
		return "Fast mode: on"
	}
	return "Fast mode: off"
}

func (m model) selfCorrectAppliedNotice() string {
	if m.selfCorrectTests {
		return "Self-correction: on"
	}
	return "Self-correction: off"
}

func (m model) turnsAppliedNotice() string {
	return fmt.Sprintf("Turn budget: %d", m.agentOptions.MaxTurns)
}

func (m model) profileAppliedNotice() string {
	return "Profile: " + displayValue(m.execProfileName, "balanced")
}

// transientNoticeDuration is deliberately long enough to read at a glance but
// short enough that routine command confirmations do not become transcript
// clutter. Details, actions, and diagnostics remain normal transcript rows.
const transientNoticeDuration = 4 * time.Second

type transientNoticeTone uint8

const (
	transientNoticeInfo transientNoticeTone = iota
	transientNoticeSuccess
	transientNoticeWarning
)

type transientNotice struct {
	text      string
	tone      transientNoticeTone
	expiresAt time.Time
}

// transientNoticeExpiredMsg is sequence-gated so an older timer can never
// clear a newer notice that replaced it.
type transientNoticeExpiredMsg struct {
	seq int
}

// showTransientNotice presents one brief, replaceable confirmation above the
// composer. It intentionally does not append a transcript row or session event:
// callers use it only for short, non-actionable outcomes.
func (m model) showTransientNotice(text string, tone transientNoticeTone) (model, tea.Cmd) {
	m, shown := m.setTransientNotice(text, tone)
	if !shown {
		return m, nil
	}
	seq := m.transientNoticeSeq
	return m, tea.Tick(transientNoticeDuration, func(time.Time) tea.Msg {
		return transientNoticeExpiredMsg{seq: seq}
	})
}

// showTransientNoticeInline is for handlers that cannot return a tea.Cmd. The
// composer blink is already a permanent, low-cost UI tick, so it clears this
// notice within one blink after expiry without introducing a second timer chain.
func (m model) showTransientNoticeInline(text string, tone transientNoticeTone) model {
	m, _ = m.setTransientNotice(text, tone)
	return m
}

func (m model) setTransientNotice(text string, tone transientNoticeTone) (model, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return m, false
	}
	m.transientNotice = transientNotice{
		text:      text,
		tone:      tone,
		expiresAt: m.noticeNow().Add(transientNoticeDuration),
	}
	m.transientNoticeSeq++
	return m, true
}

func (m model) noticeNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m model) expireTransientNotice() model {
	if expiresAt := m.transientNotice.expiresAt; !expiresAt.IsZero() && !m.noticeNow().Before(expiresAt) {
		m.transientNotice = transientNotice{}
	}
	return m
}

func (m model) transientNoticeLine(width int) string {
	notice := m.transientNotice
	if strings.TrimSpace(notice.text) == "" {
		return ""
	}
	// Keep confirmations as a quiet, text-only footer flash. A status dot makes
	// routine outcomes read like a permanent run-state badge and competes with
	// the composer status line below.
	style := zeroTheme.muted
	switch notice.tone {
	case transientNoticeSuccess:
		style = zeroTheme.ink
	case transientNoticeWarning:
		style = zeroTheme.amber
	}
	return fitStyledLine("  "+style.Render(notice.text), width)
}
