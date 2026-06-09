// Package notify emits dep-free terminal notifications (BEL and/or OSC-9
// desktop notifications) when Zero finishes a turn or needs user input.
package notify

import (
	"io"
	"strings"
	"sync"
)

// Mode selects the notification mechanism.
type Mode string

const (
	ModeOff    Mode = "off"
	ModeBell   Mode = "bell"
	ModeNotify Mode = "notify" // OSC-9 desktop notification
	ModeBoth   Mode = "both"
)

// FocusMode gates emission on terminal focus (a TUI concept).
type FocusMode string

const (
	FocusUnfocused FocusMode = "unfocused" // default: only when terminal is NOT focused
	FocusAlways    FocusMode = "always"
	FocusFocused   FocusMode = "focused"
)

// Event is the moment that triggered a notification.
type Event int

const (
	Completion Event = iota
	AwaitingInput
)

// Config is the resolved notifier policy. Zero value (Mode=="") is silent.
type Config struct {
	Mode      Mode
	FocusMode FocusMode
}

const maxMessageLen = 120

// Notifier emits notifications to w according to cfg. Safe for concurrent use.
type Notifier struct {
	w   io.Writer
	cfg Config // immutable after New; reads outside the lock are safe

	mu      sync.Mutex
	focused bool
}

// New returns a Notifier. focused defaults to false so a headless caller (no
// focus signal) still emits under the default "unfocused" focus mode; an
// interactive caller should call SetFocused(true) at launch.
func New(w io.Writer, cfg Config) *Notifier {
	return &Notifier{w: w, cfg: cfg}
}

// SetFocused records the terminal focus state (TUI FocusMsg/BlurMsg).
func (n *Notifier) SetFocused(focused bool) {
	n.mu.Lock()
	n.focused = focused
	n.mu.Unlock()
}

// Notify emits a notification for event if policy allows. message is the OSC-9
// body (ignored for bell). Write errors are intentionally ignored — a failed
// notification must never disrupt the run.
func (n *Notifier) Notify(event Event, message string) {
	if n.w == nil || n.cfg.Mode == ModeOff || n.cfg.Mode == "" {
		return
	}
	seq := sequence(n.cfg.Mode, message)
	if seq == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !shouldEmit(n.cfg, event, n.focused) {
		return
	}
	_, _ = io.WriteString(n.w, seq)
}

// DefaultMessage is the generic OSC-9 body for an event (no prompt content).
func DefaultMessage(event Event) string {
	if event == AwaitingInput {
		return "Zero: needs input"
	}
	return "Zero: ready"
}

func shouldEmit(cfg Config, _ Event, focused bool) bool {
	if cfg.Mode == ModeOff || cfg.Mode == "" {
		return false
	}
	switch cfg.FocusMode {
	case FocusAlways:
		return true
	case FocusFocused:
		return focused
	default: // FocusUnfocused, "", or unknown
		return !focused
	}
}

func sequence(mode Mode, message string) string {
	switch mode {
	case ModeBell:
		return "\x07"
	case ModeNotify:
		return "\x1b]9;" + sanitizeMessage(message) + "\x07"
	case ModeBoth:
		return "\x07\x1b]9;" + sanitizeMessage(message) + "\x07"
	default:
		return ""
	}
}

// Enabled reports whether mode will ever emit a notification.
func Enabled(mode Mode) bool {
	return mode != "" && mode != ModeOff
}

// sanitizeMessage drops control bytes (so the message can't break the escape or
// inject terminal control) and clamps to maxMessageLen runes.
func sanitizeMessage(s string) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		if r == 0x1b || r == 0x07 || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		count++
		if count >= maxMessageLen {
			break
		}
	}
	return b.String()
}
