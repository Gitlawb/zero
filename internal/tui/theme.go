package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// tuiTheme is the resolved terminal palette Zero renders with — the Lime design
// (the terminal translation of docs/design/zero_tui_lime.html). It is produced by
// buildTheme from a palette, so the same renderers serve both the dark default
// and the light variant; the active theme lives in the package var zeroTheme and
// may be swapped at startup (background detection / ZERO_THEME / --theme) or live
// via /theme. Colors are truecolor hex; lipgloss downsamples on limited displays
// and renders plain text when there is no TTY (tests). Every renderer consumes
// these named styles — no hex literal may appear outside this file.
type tuiTheme struct {
	// Base tokens.
	ink        lipgloss.Style // primary text
	muted      lipgloss.Style // secondary text, assistant interim prose
	faint      lipgloss.Style // hints, metadata
	faintest   lipgloss.Style // line numbers, separators, tool args
	accent     lipgloss.Style // brand lime: prompts, spinner, focus
	green      lipgloss.Style // success, diff add sign, ✓
	red        lipgloss.Style // errors, diff del sign, ✗, deny
	amber      lipgloss.Style // permission surfaces, warnings, auto badge
	blue       lipgloss.Style // grep file locations, local-model dot
	gitAdd     lipgloss.Style // PR/local diff additions
	gitDel     lipgloss.Style // PR/local diff deletions
	line       lipgloss.Style // default borders, rules, status separators
	lineStrong lipgloss.Style // emphasized borders
	selection  lipgloss.Style // transcript selection highlight

	// Title bar.
	badge lipgloss.Style // ` 0 ` brand chip: onAccent on accent, bold

	// Stream roles.
	userPrompt lipgloss.Style // ❯ user gutter, accent bold
	sayText    lipgloss.Style // assistant interim prose, muted

	// Tool cards.
	toolName   lipgloss.Style // head-row tool name, ink bold
	toolTarget lipgloss.Style // head-row target path, muted
	toolArg    lipgloss.Style // one-line arg hint, faintest
	autoTag    lipgloss.Style // `auto` auto-approval marker, amber
	cardRun    lipgloss.Style // card border while the call runs (accent-mixed)
	cardErr    lipgloss.Style // card border after an error (red-mixed)
	bashPrompt lipgloss.Style // ❯ command gutter inside bash cards, accent bold
	grepLoc    lipgloss.Style // file:line locations in grep bodies, blue

	// Diff bodies. The sign/count styles are bare foregrounds; the line styles
	// carry the tinted backgrounds standing in for the prototype's 9% overlays.
	// Gutter and sign columns get their own bg-carrying styles because lipgloss
	// resets the background between adjacent Render calls — every segment of a
	// tinted row must carry the tint itself.
	diffAdd    lipgloss.Style // + sign in counts
	diffDel    lipgloss.Style // − sign in counts
	diffMeta   lipgloss.Style // @@ hunks, +++/--- headers
	addLine    lipgloss.Style // added-line text: addInk on addBg
	delLine    lipgloss.Style // deleted-line text: delInk on delBg
	addLineNum lipgloss.Style // gutter number on addBg
	delLineNum lipgloss.Style // gutter number on delBg
	addSign    lipgloss.Style // + column on addBg
	delSign    lipgloss.Style // − column on delBg
	delText    lipgloss.Style // delInk as bare foreground (stderr-ish output)

	// Permission surfaces.
	permBadge  lipgloss.Style // PERMISSION chip: onAccent on amber, bold
	permBg     lipgloss.Style // permission card body tint
	permBorder lipgloss.Style // permission card border (amber-mixed line)

	// Surfaces.
	panel           lipgloss.Style // bare panel background (card padding, body fill)
	userPromptPanel lipgloss.Style // submitted user prompt background

	// Permission modes.
	modeAuto   lipgloss.Style
	modeAsk    lipgloss.Style
	modeUnsafe lipgloss.Style

	// Raw colors a few renderers paint/interpolate with directly (the streaming
	// fade interpolates accent→ink; panel-backed prompts paint on bgPanel), kept
	// on the theme so a theme switch reaches them too. The bg* colors back the
	// on* surface helpers below.
	accentColor color.Color
	inkColor    color.Color
	bgPanel     color.Color
	bgPrompt    color.Color
	bgSel       color.Color
	bgPerm      color.Color
}

// palette is the raw color-token table for one theme. buildTheme turns it into a
// resolved tuiTheme. darkPalette and lightPalette are the ONLY place hex literals
// live. All dark tints stay darker than ink so every pairing survives 256-color
// downsampling; the light set is dark-on-light with the same intent inverted.
type palette struct {
	panel    string // card backgrounds (the terminal canvas itself is never painted full-bleed)
	promptBg string // submitted user prompt background
	line     string // default borders, rules
	line2    string // emphasized borders
	ink      string // primary text
	muted    string // secondary text
	faint    string // hints, metadata
	faintest string // line numbers, separators, tool args
	accent   string // brand lime
	green    string // success, diff add
	red      string // errors, diff del
	amber    string // permission, warnings
	blue     string // grep locations, local-model dot
	gitAdd   string // footer PR diff additions
	gitDel   string // footer PR diff deletions
	addBg    string // diff added-line bg
	delBg    string // diff deleted-line bg
	permBg   string // permission card bg
	selBg    string // selected row bg
	addInk   string // added-line text
	delInk   string // deleted-line text
	onAccent string // text on accent or amber fills
	cardRun  string // running card border (accent mixed into line)
	cardErr  string // errored card border (red mixed into line)
	cardPerm string // permission card border (amber mixed into line)
}

// darkPalette is the original Lime palette: a near-black chat surface with one
// lime accent. bg (#070708) is the terminal's own canvas — deliberately never
// painted — so no token references it.
var darkPalette = palette{
	panel:    "#0e0e10",
	promptBg: "#262626",
	line:     "#242429",
	line2:    "#414147",
	ink:      "#ececee",
	muted:    "#8b8b93",
	faint:    "#838389",
	faintest: "#7c7c82",
	accent:   "#caff3f",
	green:    "#5dd1a4",
	red:      "#ff7a7a",
	amber:    "#ffc25c",
	blue:     "#7db4ff",
	gitAdd:   "#7db87a",
	gitDel:   "#b87a7a",
	addBg:    "#15201d",
	delBg:    "#241819",
	permBg:   "#1c1915",
	selBg:    "#32401b", // selected row bg — brightened from #1d2114 so the highlighted row separates from the panel (sep 1.18→1.73) while ink label contrast stays ~9.4:1
	addInk:   "#bdeed7",
	delInk:   "#f2c4c4",
	onAccent: "#000000",
	cardRun:  "#5a6b2e",
	cardErr:  "#6b3434",
	cardPerm: "#6b5a2e",
}

// lightPalette is dark-on-light: light-gray surfaces with near-black ink. The
// muted/faint/faintest grays get progressively LIGHTER (toward the surface) so the
// same text hierarchy reads on white; the lime accent is darkened to keep contrast
// against a light background, and the diff/permission tints become light surfaces.
var lightPalette = palette{
	panel:    "#ececed", // card backgrounds (slightly off-white)
	promptBg: "#dcdce0", // submitted prompt background
	line:     "#cfcfd5", // default borders
	line2:    "#b0b0b8", // emphasized borders
	ink:      "#1b1b1d", // primary text (near-black)
	muted:    "#54545b", // secondary text
	faint:    "#5b5b62", // hints, metadata
	faintest: "#646469", // line numbers, separators
	accent:   "#477006", // brand lime, darkened to AA contrast on light
	green:    "#1c7a4a", // success / diff add
	red:      "#c0322c", // errors / diff del
	amber:    "#8f6200", // permission / warnings
	blue:     "#1d5fd6", // grep locations
	gitAdd:   "#2f7a3a", // footer PR diff additions
	gitDel:   "#a83a3a", // footer PR diff deletions
	addBg:    "#e2f3e6", // diff added-line bg (light green)
	delBg:    "#fbe6e6", // diff deleted-line bg (light red)
	permBg:   "#fbf0d8", // permission card bg (light amber)
	selBg:    "#cfe78f", // selected row bg (light accent) — deepened from #e7f2cd so it separates from the near-white panel (sep 1.01→1.15); ink label stays >12:1, faint ~5:1
	addInk:   "#1d5b37", // added-line text
	delInk:   "#8e2d2d", // deleted-line text
	onAccent: "#ffffff", // text on the (dark) accent/amber fills
	cardRun:  "#94ad60", // running card border
	cardErr:  "#cf9a9a", // errored card border
	cardPerm: "#c9ad7d", // permission card border
}

// buildTheme resolves a palette into the styles every renderer uses.
func buildTheme(p palette) tuiTheme {
	col := func(s string) color.Color { return lipgloss.Color(s) }
	fg := func(s string) lipgloss.Style { return lipgloss.NewStyle().Foreground(col(s)) }
	return tuiTheme{
		ink:        fg(p.ink),
		muted:      fg(p.muted),
		faint:      fg(p.faint),
		faintest:   fg(p.faintest),
		accent:     fg(p.accent).Bold(true),
		green:      fg(p.green),
		red:        fg(p.red),
		amber:      fg(p.amber),
		blue:       fg(p.blue),
		gitAdd:     fg(p.gitAdd),
		gitDel:     fg(p.gitDel),
		line:       fg(p.line),
		lineStrong: fg(p.line2),
		selection:  lipgloss.NewStyle().Background(col(p.accent)).Foreground(col(p.onAccent)),

		badge: lipgloss.NewStyle().Background(col(p.accent)).Foreground(col(p.onAccent)).Bold(true),

		userPrompt: fg(p.accent).Bold(true),
		sayText:    fg(p.muted),
		toolName:   fg(p.ink).Bold(true),
		toolTarget: fg(p.muted),
		toolArg:    fg(p.faintest),
		autoTag:    fg(p.amber),
		cardRun:    fg(p.cardRun),
		cardErr:    fg(p.cardErr),
		bashPrompt: fg(p.accent).Bold(true),
		grepLoc:    fg(p.blue),

		diffAdd:    fg(p.green),
		diffDel:    fg(p.red),
		diffMeta:   fg(p.faintest),
		addLine:    lipgloss.NewStyle().Foreground(col(p.addInk)).Background(col(p.addBg)),
		delLine:    lipgloss.NewStyle().Foreground(col(p.delInk)).Background(col(p.delBg)),
		addLineNum: lipgloss.NewStyle().Foreground(col(p.faintest)).Background(col(p.addBg)),
		delLineNum: lipgloss.NewStyle().Foreground(col(p.faintest)).Background(col(p.delBg)),
		addSign:    lipgloss.NewStyle().Foreground(col(p.green)).Background(col(p.addBg)),
		delSign:    lipgloss.NewStyle().Foreground(col(p.red)).Background(col(p.delBg)),
		delText:    fg(p.delInk),

		permBadge:  lipgloss.NewStyle().Background(col(p.amber)).Foreground(col(p.onAccent)).Bold(true),
		permBg:     lipgloss.NewStyle().Background(col(p.permBg)),
		permBorder: fg(p.cardPerm),

		panel:           lipgloss.NewStyle().Background(col(p.panel)),
		userPromptPanel: lipgloss.NewStyle().Background(col(p.promptBg)),

		modeAuto:   fg(p.green).Bold(true),
		modeAsk:    fg(p.amber).Bold(true),
		modeUnsafe: fg(p.red).Bold(true),

		accentColor: col(p.accent),
		inkColor:    col(p.ink),
		bgPanel:     col(p.panel),
		bgPrompt:    col(p.promptBg),
		bgSel:       col(p.selBg),
		bgPerm:      col(p.permBg),
	}
}

// zeroTheme is the active palette every renderer reads. It defaults to dark and is
// reassigned by theme selection at startup and by /theme. All references are
// .field accesses evaluated at render time, so reassigning this var repaints every
// subsequent render.
var zeroTheme = buildTheme(darkPalette)

// onPanel returns a copy of style that paints on the panel surface. lipgloss
// resets the background between adjacent Render calls, so every segment of a
// panel row (including padding) must carry the background itself — renderers wrap
// their foreground styles through this instead of referencing hex.
func (t tuiTheme) onPanel(style lipgloss.Style) lipgloss.Style {
	return style.Background(t.bgPanel)
}

// onUserPrompt paints on the submitted user-prompt surface.
func (t tuiTheme) onUserPrompt(style lipgloss.Style) lipgloss.Style {
	return style.Background(t.bgPrompt)
}

// onSel paints on the selected-row tint.
func (t tuiTheme) onSel(style lipgloss.Style) lipgloss.Style {
	return style.Background(t.bgSel)
}

// onPerm paints on the permission-card tint.
func (t tuiTheme) onPerm(style lipgloss.Style) lipgloss.Style {
	return style.Background(t.bgPerm)
}
