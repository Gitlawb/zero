package tui

import (
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/notify"
)

// notifyChoice is one row in the /notify picker: a (mode, focusMode) pair and
// the label the user reads.
type notifyChoice struct {
	label     string
	mode      string
	focusMode string
}

// notifyChoiceSubtitle renders the human explanation shown as the picker row's
// Meta text: "<what the mode does> — <when the focus fires>".
func (c notifyChoice) subtitle() string {
	modeDescriptions := map[string]string{
		string(notify.ModeOff):    "silent",
		string(notify.ModeBell):   "terminal bell only",
		string(notify.ModeNotify): "desktop notification only",
		string(notify.ModeBoth):   "terminal bell + desktop notification",
	}
	focusDescriptions := map[string]string{
		string(notify.FocusUnfocused): "only when the terminal is in the background",
		string(notify.FocusAlways):    "every time",
		string(notify.FocusFocused):   "only while the terminal is focused",
	}
	return modeDescriptions[c.mode] + " — " + focusDescriptions[c.focusMode]
}

// notifyPickerChoices enumerates the FULL mode x focus space (4 modes x 3
// focus modes = 12 rows), the way newThemePicker enumerates every theme. The
// earlier 4-row curated list could not represent the other 8 valid pairs, so
// opening the picker on one of them fell through to row 0 and Enter silently
// committed a different setting than the user's current one (maintainer
// review, PR #1001). Every valid pair must have a row so Enter always keeps
// (or explicitly changes) the user's actual setting.
func notifyPickerChoices() []notifyChoice {
	modes := []string{string(notify.ModeBoth), string(notify.ModeBell), string(notify.ModeNotify), string(notify.ModeOff)}
	foci := []string{string(notify.FocusUnfocused), string(notify.FocusAlways), string(notify.FocusFocused)}
	choices := make([]notifyChoice, 0, len(modes)*len(foci))
	for _, mode := range modes {
		for _, focus := range foci {
			choices = append(choices, notifyChoice{
				label:     mode + " · " + focus,
				mode:      mode,
				focusMode: focus,
			})
		}
	}
	return choices
}

// handleNotifyCommand implements /notify [list|off|bell|notify|both [focus]].
// Bare `/notify` opens the picker at the dispatch layer. A mode-only argument
// preserves the focusMode stored in the USER'S OWN config (blank stays blank);
// seeding from the model's in-session value would copy a project config's
// choice, or a pinned default, into the user's global file. Mirrors
// handleThemeCommand.
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
	} else if stored, err := m.storedNotify(); err == nil {
		// Preserve what the USER chose (blank included); never the resolved
		// view, which merges project config and in-session defaults.
		focus = stored.FocusMode
	}
	m.notifyMode = mode
	m.notifyFocusMode = focus
	// Apply to the live notifier so the change takes effect on the next
	// permission prompt in this session, not just after a restart. A blank
	// focusMode is fine here: the notifier treats blank as "unfocused".
	if m.notifier != nil {
		m.notifier.Configure(notify.Config{
			Mode:      notify.Mode(mode),
			FocusMode: notify.FocusMode(focus),
		})
	}
	lines := []string{
		"Notify",
		"active mode: " + mode + ", focus: " + effectiveFocusLabel(focus),
	}
	if note := m.persistNotifyPreference(mode, focus); note != "" {
		lines = append(lines, note)
	}
	return m, strings.Join(lines, "\n")
}

// storedNotify reads the notify block from the user's own config file. Missing
// file or read error returns the zero value (best-effort, like the rest of the
// preference persistence).
func (m model) storedNotify() (config.NotifyConfig, error) {
	if strings.TrimSpace(m.userConfigPath) == "" {
		return config.NotifyConfig{}, nil
	}
	return config.UserNotify(m.userConfigPath)
}

// effectiveFocusLabel renders a focus value for the state line: blank means the
// built-in "unfocused" default, so say so instead of showing an empty string.
func effectiveFocusLabel(focus string) string {
	if strings.TrimSpace(focus) == "" {
		return "unfocused (default)"
	}
	return focus
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

// notifyStateText renders the /notify state view: the stored preference plus
// every valid pair, so the user has the same information whether they ran
// `/notify list` or just opened the picker.
func (m model) notifyStateText() string {
	stored, _ := m.storedNotify()
	mode := stored.Mode
	if strings.TrimSpace(mode) == "" {
		mode = string(effectiveTUINotifyMode(mode))
	}
	sections := []commandSection{{
		Title: "State",
		Lines: []string{
			"active mode: " + mode,
			"active focus: " + effectiveFocusLabel(stored.FocusMode),
		},
	}}
	rows := make([]string, 0, 12)
	for _, c := range notifyPickerChoices() {
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
