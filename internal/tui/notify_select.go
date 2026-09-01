package tui

import (
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/notify"
)

// notifyChoice is one row in the /notify picker. The mode and focusMode pair is
// the on-disk shape; the label is what the user reads.
type notifyChoice struct {
	label     string
	subtitle  string
	mode      string
	focusMode string
}

// notifyChoices is the ordered list shown by the /notify picker. "Unfocused +
// both" (the resolver default) is first because it is the most useful option
// for users who do not already have a strong opinion. "Silent" is last so the
// recommended path is also the visually-defaulted one. Adding a new (mode,
// focus) pair is enough to extend the picker and the /notify state list.
var notifyChoices = []notifyChoice{
	{
		label:     "Notify when unfocused (recommended)",
		subtitle:  "Sound + desktop notification when the terminal is in the background.",
		mode:      string(notify.ModeBoth),
		focusMode: string(notify.FocusUnfocused),
	},
	{
		label:     "Always notify",
		subtitle:  "Sound + desktop notification every time Zero needs your input.",
		mode:      string(notify.ModeBoth),
		focusMode: string(notify.FocusAlways),
	},
	{
		label:     "Bell only",
		subtitle:  "Terminal bell (no desktop notification) every time Zero needs your input.",
		mode:      string(notify.ModeBell),
		focusMode: string(notify.FocusAlways),
	},
	{
		label:     "Silent",
		subtitle:  "Show prompts in the TUI only — no extra sound or notification.",
		mode:      string(notify.ModeOff),
		focusMode: string(notify.FocusUnfocused),
	},
}

// handleNotifyCommand implements /notify [list|off|bell|notify|both [focus]].
// Bare `/notify` opens the picker at the dispatch layer; a mode-only argument
// keeps the existing focusMode. Mirrors handleThemeCommand.
func (m model) handleNotifyCommand(args string) (model, string) {
	tokens := strings.Fields(strings.TrimSpace(args))
	if len(tokens) == 0 || tokens[0] == "list" {
		return m, m.notifyStateText()
	}
	if len(tokens) > 2 {
		return m, "Notify\nToo many arguments: " + args + " (usage: /notify <off|bell|notify|both> [unfocused|always|focused])"
	}
	mode := strings.ToLower(strings.TrimSpace(tokens[0]))
	if !isValidNotifyMode(mode) {
		return m, "Notify\nUnknown mode: " + tokens[0] + " (expected off, bell, notify, or both; run /notify with no argument to pick from the list)"
	}
	focus := ""
	if len(tokens) > 1 {
		focus = strings.ToLower(strings.TrimSpace(tokens[1]))
		if !isValidNotifyFocusMode(focus) {
			return m, "Notify\nUnknown focus mode: " + tokens[1] + " (expected unfocused, always, or focused)"
		}
	} else {
		focus = m.notifyCurrentFocusMode()
	}
	m.notifyMode = mode
	m.notifyFocusMode = focus
	// Apply to the live notifier so the change takes effect on the next
	// permission prompt in this session, not just after a restart.
	if m.notifier != nil {
		m.notifier.Configure(notify.Config{
			Mode:      notify.Mode(mode),
			FocusMode: notify.FocusMode(focus),
		})
	}
	lines := []string{
		"Notify",
		"active mode: " + mode + ", focus: " + focus,
	}
	if note := m.persistNotifyPreference(mode, focus); note != "" {
		lines = append(lines, note)
	}
	return m, strings.Join(lines, "\n")
}

// persistNotifyPreference writes the choice to user config so it survives a
// restart. Best-effort: returns a short note to surface on failure, or "" on
// success / when there is no config path (e.g. tests).
func (m model) persistNotifyPreference(mode string, focus string) string {
	if strings.TrimSpace(m.userConfigPath) == "" {
		return ""
	}
	if _, err := config.SetNotify(m.userConfigPath, config.NotifyConfig{
		Mode:      mode,
		FocusMode: focus,
	}); err != nil {
		return "note: could not save notify preference (" + err.Error() + ")"
	}
	return ""
}

// notifyStateText renders the /notify state view: current mode + focus + the
// picker rows, so the user has the same information whether they ran
// `/notify list` or just opened the picker.
func (m model) notifyStateText() string {
	activeMode := m.notifyCurrentMode()
	activeFocus := m.notifyCurrentFocusMode()
	sections := []commandSection{{
		Title: "State",
		Lines: []string{
			"active mode: " + activeMode,
			"active focus: " + activeFocus,
		},
	}}
	rows := make([]string, 0, len(notifyChoices))
	for _, c := range notifyChoices {
		rows = append(rows, c.label)
	}
	sections = append(sections, commandSection{
		Title: "Available",
		Lines: rows,
	})
	return renderCommandOutput(commandOutput{
		Title:    "Notify",
		Status:   commandStatusOK,
		Sections: sections,
		Hints:    []string{"run /notify with no argument to open the picker, or /notify <mode> [focus] to change directly"},
	})
}

// notifyCurrentMode and notifyCurrentFocusMode return the in-session notify
// preference. newModel populates both from options.Notify via
// effectiveTUINotifyMode (which never returns ""), so no empty fallback is
// needed here.
func (m model) notifyCurrentMode() string {
	return m.notifyMode
}

func (m model) notifyCurrentFocusMode() string {
	return m.notifyFocusMode
}

// isValidNotifyMode reports whether s names one of the four notification modes.
func isValidNotifyMode(s string) bool {
	switch s {
	case string(notify.ModeOff), string(notify.ModeBell), string(notify.ModeNotify), string(notify.ModeBoth):
		return true
	}
	return false
}

// isValidNotifyFocusMode reports whether s names one of the three focus modes.
func isValidNotifyFocusMode(s string) bool {
	switch s {
	case string(notify.FocusUnfocused), string(notify.FocusAlways), string(notify.FocusFocused):
		return true
	}
	return false
}
