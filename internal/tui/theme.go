package tui

import "github.com/charmbracelet/lipgloss"

// tuiTheme is the single source of truth for Zero's terminal palette — the
// Lime design: a near-black chat surface with one lime accent (the terminal
// translation of docs/design/zero_tui_lime.html). Colors are truecolor hex so
// the palette renders consistently across terminals; lipgloss downsamples
// automatically on limited displays and renders plain text when there is no
// TTY (e.g. during tests). Every renderer consumes these named styles — no hex
// literal may appear outside this file.
type tuiTheme struct {
	// Base tokens.
	ink        lipgloss.Style // primary text
	muted      lipgloss.Style // secondary text, assistant interim prose
	faint      lipgloss.Style // hints, metadata
	faintest   lipgloss.Style // line numbers, separators, tool args
	accent     lipgloss.Style // brand lime: prompts, spinner, focus, final rail
	green      lipgloss.Style // success, diff add sign, ✓
	red        lipgloss.Style // errors, diff del sign, ✗, deny
	amber      lipgloss.Style // permission surfaces, warnings, auto badge
	blue       lipgloss.Style // grep file locations, local-model dot
	line       lipgloss.Style // default borders, rules, status separators
	lineStrong lipgloss.Style // emphasized borders
	selection  lipgloss.Style // transcript selection highlight

	// Title bar.
	badge lipgloss.Style // ` 0 ` brand chip: onAccent on accent, bold

	// Stream roles.
	userPrompt lipgloss.Style // ❯ user gutter, accent bold
	sayText    lipgloss.Style // assistant interim prose, muted
	finalRail  lipgloss.Style // │ gutter rail on the final answer, accent

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
	permRisk   lipgloss.Style // risk: <level> readout, amber
	permBg     lipgloss.Style // permission card body tint
	permBorder lipgloss.Style // permission card border (amber-mixed line)

	// Surfaces.
	panel lipgloss.Style // bare panel background (card padding, body fill)

	// Permission modes.
	modeAuto   lipgloss.Style
	modeAsk    lipgloss.Style
	modeUnsafe lipgloss.Style
}

// The Lime token table. bg (#070708) is the terminal's own canvas — it is
// deliberately never painted full-bleed, so no style references it. Terminals
// cannot alpha-blend or glow: the four solid tint tokens (addBg/delBg/permBg/
// selBg) ARE the translation of the prototype's rgba overlays, and the two
// card-border mixes stand in for its focus glow. All tints stay darker than
// ink so every pairing survives 256-color downsampling.
const (
	colorPanel    = "#0e0e10" // card backgrounds
	colorPanel2   = "#121215" // card header rows, picker rows
	colorPanel3   = "#17171b" // selected/hovered row bg
	colorLine     = "#242429" // default borders, rules
	colorLine2    = "#414147" // emphasized borders
	colorInk      = "#ececee" // primary text
	colorMuted    = "#8b8b93" // secondary text
	colorFaint    = "#5b5b63" // hints, metadata
	colorFaintest = "#3a3a40" // line numbers, separators, tool args
	colorAccent   = "#caff3f" // brand lime
	colorGreen    = "#5dd1a4" // success, diff add
	colorRed      = "#ff7a7a" // errors, diff del
	colorAmber    = "#ffc25c" // permission, warnings
	colorBlue     = "#7db4ff" // grep locations, local-model dot
	colorAddBg    = "#15201d" // diff added-line bg (green @9% over panel)
	colorDelBg    = "#241819" // diff deleted-line bg (red @9%)
	colorPermBg   = "#1c1915" // permission card bg (amber @6%)
	colorSelBg    = "#1d2114" // selected row bg (accent @8%)
	colorAddInk   = "#bdeed7" // added-line text
	colorDelInk   = "#f2c4c4" // deleted-line text
	colorOnAccent = "#000000" // text on accent or amber fills
	colorCardRun  = "#5a6b2e" // running card border (accent mixed into line)
	colorCardErr  = "#6b3434" // errored card border (red mixed into line)
	colorCardPerm = "#6b5a2e" // permission card border (amber mixed into line)
)

var zeroTheme = tuiTheme{
	ink:        lipgloss.NewStyle().Foreground(lipgloss.Color(colorInk)),
	muted:      lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)),
	faint:      lipgloss.NewStyle().Foreground(lipgloss.Color(colorFaint)),
	faintest:   lipgloss.NewStyle().Foreground(lipgloss.Color(colorFaintest)),
	accent:     lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)).Bold(true),
	green:      lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)),
	red:        lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)),
	amber:      lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber)),
	blue:       lipgloss.NewStyle().Foreground(lipgloss.Color(colorBlue)),
	line:       lipgloss.NewStyle().Foreground(lipgloss.Color(colorLine)),
	lineStrong: lipgloss.NewStyle().Foreground(lipgloss.Color(colorLine2)),
	selection:  lipgloss.NewStyle().Background(lipgloss.Color(colorAccent)).Foreground(lipgloss.Color(colorOnAccent)),

	badge: lipgloss.NewStyle().Background(lipgloss.Color(colorAccent)).Foreground(lipgloss.Color(colorOnAccent)).Bold(true),

	userPrompt: lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)).Bold(true),
	sayText:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)),
	finalRail:  lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)),

	toolName:   lipgloss.NewStyle().Foreground(lipgloss.Color(colorInk)).Bold(true),
	toolTarget: lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)),
	toolArg:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorFaintest)),
	autoTag:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber)),
	cardRun:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorCardRun)),
	cardErr:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorCardErr)),
	bashPrompt: lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)).Bold(true),
	grepLoc:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorBlue)),

	diffAdd:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)),
	diffDel:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)),
	diffMeta:   lipgloss.NewStyle().Foreground(lipgloss.Color(colorFaintest)),
	addLine:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorAddInk)).Background(lipgloss.Color(colorAddBg)),
	delLine:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorDelInk)).Background(lipgloss.Color(colorDelBg)),
	addLineNum: lipgloss.NewStyle().Foreground(lipgloss.Color(colorFaintest)).Background(lipgloss.Color(colorAddBg)),
	delLineNum: lipgloss.NewStyle().Foreground(lipgloss.Color(colorFaintest)).Background(lipgloss.Color(colorDelBg)),
	addSign:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)).Background(lipgloss.Color(colorAddBg)),
	delSign:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)).Background(lipgloss.Color(colorDelBg)),
	delText:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorDelInk)),

	permBadge:  lipgloss.NewStyle().Background(lipgloss.Color(colorAmber)).Foreground(lipgloss.Color(colorOnAccent)).Bold(true),
	permRisk:   lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber)),
	permBg:     lipgloss.NewStyle().Background(lipgloss.Color(colorPermBg)),
	permBorder: lipgloss.NewStyle().Foreground(lipgloss.Color(colorCardPerm)),

	panel: lipgloss.NewStyle().Background(lipgloss.Color(colorPanel)),

	modeAuto:   lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)).Bold(true),
	modeAsk:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber)).Bold(true),
	modeUnsafe: lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)).Bold(true),
}

// onPanel returns a copy of style that paints on the panel surface. lipgloss
// resets the background between adjacent Render calls, so every segment of a
// panel row (including padding) must carry the background itself — renderers
// wrap their foreground styles through this instead of referencing hex.
func (t tuiTheme) onPanel(style lipgloss.Style) lipgloss.Style {
	return style.Background(lipgloss.Color(colorPanel))
}

// onPanel2 paints on the header/picker-row surface.
func (t tuiTheme) onPanel2(style lipgloss.Style) lipgloss.Style {
	return style.Background(lipgloss.Color(colorPanel2))
}

// onSel paints on the selected-row tint.
func (t tuiTheme) onSel(style lipgloss.Style) lipgloss.Style {
	return style.Background(lipgloss.Color(colorSelBg))
}

// onPerm paints on the permission-card tint.
func (t tuiTheme) onPerm(style lipgloss.Style) lipgloss.Style {
	return style.Background(lipgloss.Color(colorPermBg))
}
