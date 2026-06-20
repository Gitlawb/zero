package tui

import "strings"

// themeMode is the operator's palette preference.
type themeMode string

const (
	themeAuto  themeMode = "auto" // detect terminal background (default)
	themeDark  themeMode = "dark"
	themeLight themeMode = "light"
)

// themeModes lists the values /theme accepts.
var themeModes = []string{string(themeAuto), string(themeDark), string(themeLight)}

// resolveThemeMode picks the preference in precedence order: an explicit value
// (the --theme flag, threaded via Options.Theme), then ZERO_THEME, then auto.
func resolveThemeMode(flagValue, envValue string) themeMode {
	for _, v := range []string{flagValue, envValue} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "dark":
			return themeDark
		case "light":
			return themeLight
		case "auto":
			return themeAuto
		}
	}
	return themeAuto
}

// validThemeMode reports whether s names a theme mode (for /theme validation).
func validThemeMode(s string) bool {
	switch themeMode(strings.ToLower(strings.TrimSpace(s))) {
	case themeAuto, themeDark, themeLight:
		return true
	}
	return false
}

// applyTheme swaps the active palette (zeroTheme) and the globals derived from it
// — the streaming-fade ramp and the static render cache — so a switch repaints
// every subsequent render. For themeAuto it resolves to dark/light from
// hasDarkBackground; explicit dark/light ignore it. Returns the concrete mode
// applied (never auto). Must run on the Bubble Tea update goroutine (or before the
// program starts), like every other zeroTheme access.
func applyTheme(mode themeMode, hasDarkBackground bool) themeMode {
	resolved := mode
	if mode == themeAuto {
		resolved = themeDark
		if !hasDarkBackground {
			resolved = themeLight
		}
	}
	switch resolved {
	case themeLight:
		zeroTheme = buildTheme(lightPalette)
	default:
		zeroTheme = buildTheme(darkPalette)
	}
	rebuildStreamingFadePalette()
	if defaultRenderCache != nil {
		defaultRenderCache.clear() // old-palette entries must not be reused
	}
	return resolved
}

// handleThemeCommand implements /theme [auto|dark|light]: no arg shows state, a
// valid mode switches the active palette live. Mirrors handleStyleCommand.
func (m model) handleThemeCommand(args string) (model, string) {
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg == "" || arg == "list" {
		return m, m.themeStateText()
	}
	if !validThemeMode(arg) {
		return m, "Theme\nUnknown theme: " + arg + " (use auto, dark, or light)"
	}
	m.themeMode = themeMode(arg)
	resolved := applyTheme(m.themeMode, m.hasDarkBg)
	active := arg
	if m.themeMode == themeAuto {
		active = "auto (" + string(resolved) + ")"
	}
	return m, strings.Join([]string{
		"Theme",
		"active theme: " + active,
		"Already-printed scrollback keeps its previous colors; new output uses the new theme.",
	}, "\n")
}

// themeStateText renders the /theme state view.
func (m model) themeStateText() string {
	active := string(m.themeMode)
	if m.themeMode == themeAuto {
		bg := "light"
		if m.hasDarkBg {
			bg = "dark"
		}
		active = "auto (" + bg + ")"
	}
	return renderCommandOutput(commandOutput{
		Title:  "Theme",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "State",
			Lines: []string{
				"active theme: " + active,
				"available: " + strings.Join(themeModes, ", "),
			},
		}},
		Hints: []string{"use /theme <auto|dark|light> to switch this TUI session"},
	})
}
