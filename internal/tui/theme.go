package tui

import (
	"fmt"
	"image/color"
	"strconv"

	"charm.land/lipgloss/v2"
)

// tuiTheme is the resolved terminal palette Zero renders with. The default system
// theme keeps the terminal's foreground and background intact; named palettes only
// style local UI surfaces. The active theme lives in zeroTheme and changes only
// after an explicit /theme selection. Every renderer consumes these named styles —
// no hex literal may appear outside theme_palettes.go (the palette tables + theme
// registry).
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
	hover      lipgloss.Style // hovered clickable row (specialist card, toggle, sidebar/plan row)

	// Title bar.
	badge lipgloss.Style // ` 0 ` brand chip: onAccent on accent, bold

	// Stream roles.
	userPrompt lipgloss.Style // ❯ user gutter, accent bold
	sayText    lipgloss.Style // assistant interim prose, muted

	// Tool cards.
	toolName   lipgloss.Style // head-row tool name, ink bold
	toolTarget lipgloss.Style // head-row target path, ink
	toolArg    lipgloss.Style // one-line arg hint, faintest
	cardRun    lipgloss.Style // card border while the call runs (accent-mixed)
	cardErr    lipgloss.Style // card border after an error (red-mixed)
	bashPrompt lipgloss.Style // ❯ command gutter inside bash cards, accent bold
	grepLoc    lipgloss.Style // file:line locations in grep bodies, blue

	// Diff bodies. The sign/count styles are bare foregrounds; the line styles
	// carry the tinted backgrounds standing in for the prototype's 9% overlays.
	// Gutter and sign columns get their own bg-carrying styles because lipgloss
	// resets the background between adjacent Render calls — every segment of a
	// tinted row must carry the tint itself.
	diffAdd     lipgloss.Style // + sign in counts
	diffDel     lipgloss.Style // − sign in counts
	diffMeta    lipgloss.Style // @@ hunks, +++/--- headers
	addLine     lipgloss.Style // added-line text: addInk on addBg
	delLine     lipgloss.Style // deleted-line text: delInk on delBg
	addLineWord lipgloss.Style // the changed span within an added line: addInk on the brighter addBgWord
	delLineWord lipgloss.Style // the changed span within a deleted line: delInk on the brighter delBgWord
	addLineNum  lipgloss.Style // gutter number on addBg
	delLineNum  lipgloss.Style // gutter number on delBg
	addSign     lipgloss.Style // + column on addBg
	delSign     lipgloss.Style // − column on delBg
	delText     lipgloss.Style // delInk as bare foreground (stderr-ish output)

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
	modePlan   lipgloss.Style

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
// resolved tuiTheme. The palette literals and the ordered theme registry live in
// theme_palettes.go (the only place hex literals live). Dark palettes keep tints
// darker than ink so every pairing survives 256-color downsampling; light palettes
// are dark-on-light with the same intent inverted.
type palette struct {
	panel     string // card backgrounds (the terminal canvas itself is never painted full-bleed)
	promptBg  string // submitted user prompt background
	line      string // default borders, rules
	line2     string // emphasized borders
	ink       string // primary text
	muted     string // secondary text
	faint     string // hints, metadata
	faintest  string // line numbers, separators, tool args
	accent    string // brand lime
	green     string // success, diff add
	red       string // errors, diff del
	amber     string // permission, warnings
	blue      string // grep locations, local-model dot
	gitAdd    string // footer PR diff additions
	gitDel    string // footer PR diff deletions
	addBg     string // diff added-line bg
	delBg     string // diff deleted-line bg
	addBgWord string // diff added-line changed-span bg (brighter than addBg)
	delBgWord string // diff deleted-line changed-span bg (brighter than delBg)
	permBg    string // permission card bg
	selBg     string // selected row bg
	addInk    string // added-line text
	delInk    string // deleted-line text
	onAccent  string // text on accent or amber fills
	cardRun   string // running card border (accent mixed into line)
	cardErr   string // errored card border (red mixed into line)
	cardPerm  string // permission card border (amber mixed into line)
}

// buildTheme resolves a named palette into the styles every renderer uses. Palette
// backgrounds are intentionally limited to local cards and diff rows; View never
// paints the full terminal canvas.
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
		// A full block (like selection) would be too heavy for something that
		// repaints on every mouse movement; a soft foreground reads as
		// "interactive" at a glance without competing with real content colors
		// (e.g. a specialist card's red/green status glyph). Amber (not the brand
		// lime accent) — lime read as too glaring for something that repaints
		// continuously as the cursor moves.
		hover: fg(p.amber),

		badge: lipgloss.NewStyle().Background(col(p.accent)).Foreground(col(p.onAccent)).Bold(true),

		userPrompt: fg(p.accent).Bold(true),
		sayText:    fg(p.muted),
		toolName:   fg(p.ink).Bold(true),
		toolTarget: fg(p.ink),
		toolArg:    fg(p.faintest),
		cardRun:    fg(p.cardRun),
		cardErr:    fg(p.cardErr),
		bashPrompt: fg(p.accent).Bold(true),
		grepLoc:    fg(p.blue),

		diffAdd:     fg(p.green),
		diffDel:     fg(p.red),
		diffMeta:    fg(p.faintest),
		addLineWord: lipgloss.NewStyle().Foreground(col(p.addInk)).Background(col(p.addBgWord)),
		delLineWord: lipgloss.NewStyle().Foreground(col(p.delInk)).Background(col(p.delBgWord)),
		addLine:     lipgloss.NewStyle().Foreground(col(p.addInk)).Background(col(p.addBg)),
		delLine:     lipgloss.NewStyle().Foreground(col(p.delInk)).Background(col(p.delBg)),
		addLineNum:  lipgloss.NewStyle().Foreground(col(p.faintest)).Background(col(p.addBg)),
		delLineNum:  lipgloss.NewStyle().Foreground(col(p.faintest)).Background(col(p.delBg)),
		addSign:     lipgloss.NewStyle().Foreground(col(p.green)).Background(col(p.addBg)),
		delSign:     lipgloss.NewStyle().Foreground(col(p.red)).Background(col(p.delBg)),
		delText:     fg(p.delInk),

		permBadge:  lipgloss.NewStyle().Background(col(p.amber)).Foreground(col(p.onAccent)).Bold(true),
		permBg:     lipgloss.NewStyle().Background(col(p.permBg)),
		permBorder: fg(p.cardPerm),

		panel:           lipgloss.NewStyle().Background(col(p.panel)),
		userPromptPanel: lipgloss.NewStyle().Background(col(p.promptBg)),

		modeAuto: fg(p.green).Bold(true),
		// The steady "ask" footer label is calm (un-bolded) so it doesn't compete
		// with the transient bold-amber permission badge (permBadge) shown when a
		// tool is actually asking right now — a glance separates state from event.
		modeAsk:    fg(p.amber),
		modeUnsafe: fg(p.red).Bold(true),
		modePlan:   fg(p.blue).Bold(true),

		accentColor: col(p.accent),
		inkColor:    col(p.ink),
		bgPanel:     col(p.panel),
		bgPrompt:    col(p.promptBg),
		bgSel:       col(p.selBg),
		bgPerm:      col(p.permBg),
	}
}

// paletteForTerminal keeps a palette readable without changing the terminal's
// canvas. A light palette on a dark terminal (or the reverse) mirrors its colors,
// preserving the palette's relationships while switching it to the terminal's
// contrast direction. This lets every listed theme remain usable with transparent
// terminals and wallpapers.
func paletteForTerminal(p palette, paletteDark, terminalDark bool) palette {
	if paletteDark == terminalDark {
		return p
	}
	return invertedPalette(p)
}

func invertedPalette(p palette) palette {
	p.ink = invertPaletteColor(p.ink)
	p.muted = invertPaletteColor(p.muted)
	p.faint = invertPaletteColor(p.faint)
	p.faintest = invertPaletteColor(p.faintest)
	p.accent = invertPaletteColor(p.accent)
	p.green = invertPaletteColor(p.green)
	p.red = invertPaletteColor(p.red)
	p.amber = invertPaletteColor(p.amber)
	p.blue = invertPaletteColor(p.blue)
	p.gitAdd = invertPaletteColor(p.gitAdd)
	p.gitDel = invertPaletteColor(p.gitDel)
	p.line = invertPaletteColor(p.line)
	p.line2 = invertPaletteColor(p.line2)
	p.panel = invertPaletteColor(p.panel)
	p.promptBg = invertPaletteColor(p.promptBg)
	p.permBg = invertPaletteColor(p.permBg)
	p.addBg = invertPaletteColor(p.addBg)
	p.delBg = invertPaletteColor(p.delBg)
	p.addBgWord = invertPaletteColor(p.addBgWord)
	p.delBgWord = invertPaletteColor(p.delBgWord)
	p.selBg = invertPaletteColor(p.selBg)
	p.addInk = invertPaletteColor(p.addInk)
	p.delInk = invertPaletteColor(p.delInk)
	p.onAccent = invertPaletteColor(p.onAccent)
	p.cardRun = invertPaletteColor(p.cardRun)
	p.cardErr = invertPaletteColor(p.cardErr)
	p.cardPerm = invertPaletteColor(p.cardPerm)
	return p
}

func invertPaletteColor(value string) string {
	if len(value) != 7 || value[0] != '#' {
		return value
	}
	rgb, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		return value
	}
	return fmt.Sprintf("#%06x", 0xffffff^rgb)
}

// buildSystemTheme preserves the terminal canvas and its foreground color. It uses
// a small ANSI role set for semantic cues, which follows the user's terminal
// palette rather than imposing a second opaque color scheme on top of it.
func buildSystemTheme() tuiTheme {
	base := lipgloss.NewStyle()
	accentColor := lipgloss.Color("6")
	greenColor := lipgloss.Color("2")
	redColor := lipgloss.Color("1")
	amberColor := lipgloss.Color("3")
	blueColor := lipgloss.Color("4")
	noColor := lipgloss.NoColor{}
	accent := lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	green := lipgloss.NewStyle().Foreground(greenColor)
	red := lipgloss.NewStyle().Foreground(redColor)
	amber := lipgloss.NewStyle().Foreground(amberColor)
	blue := lipgloss.NewStyle().Foreground(blueColor)
	return tuiTheme{
		ink:        base,
		muted:      base.Faint(true),
		faint:      base.Faint(true),
		faintest:   base.Faint(true),
		accent:     accent,
		green:      green,
		red:        red,
		amber:      amber,
		blue:       blue,
		gitAdd:     green,
		gitDel:     red,
		line:       base.Faint(true),
		lineStrong: base.Faint(true),
		selection:  base.Reverse(true),
		hover:      base.Underline(true),

		badge: base.Foreground(accentColor).Bold(true),

		userPrompt: base.Foreground(accentColor).Bold(true),
		sayText:    base.Faint(true),
		toolName:   base.Bold(true),
		toolTarget: base,
		toolArg:    base.Faint(true),
		cardRun:    accent,
		cardErr:    red,
		bashPrompt: accent,
		grepLoc:    blue,

		diffAdd:     green,
		diffDel:     red,
		diffMeta:    base.Faint(true),
		addLineWord: green.Bold(true).Underline(true),
		delLineWord: red.Bold(true).Underline(true),
		addLine:     green,
		delLine:     red,
		addLineNum:  base.Faint(true),
		delLineNum:  base.Faint(true),
		addSign:     green,
		delSign:     red,
		delText:     red,

		permBadge:  amber.Bold(true),
		permBg:     base,
		permBorder: amber,

		panel:           base,
		userPromptPanel: base,

		modeAuto:   green.Bold(true),
		modeAsk:    amber,
		modeUnsafe: red.Bold(true),
		modePlan:   blue.Bold(true),

		accentColor: accentColor,
		inkColor:    noColor,
		bgPanel:     noColor,
		bgPrompt:    noColor,
		bgSel:       noColor,
		bgPerm:      noColor,
	}
}

// zeroTheme is the active palette every renderer reads. Run applies the terminal-
// native system default before it creates the interactive program; the dark palette
// here keeps package-level render helpers deterministic before a model has started
// (including in unit tests that construct more than one model).
var zeroTheme = buildTheme(darkPalette)

// onPanel returns a copy of style that paints on the panel surface. lipgloss
// resets the background between adjacent Render calls, so every segment of a
// panel row (including padding) must carry the background itself — renderers wrap
// their foreground styles through this instead of referencing hex.
func (t tuiTheme) onPanel(style lipgloss.Style) lipgloss.Style {
	return style.Background(t.bgPanel)
}

// onSel paints on the selected-row tint.
func (t tuiTheme) onSel(style lipgloss.Style) lipgloss.Style {
	return style.Background(t.bgSel)
}
