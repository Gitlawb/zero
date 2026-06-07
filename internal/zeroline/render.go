package zeroline

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Header is the live session context shown on both surfaces.
type Header struct {
	Cwd, Branch, Model, Provider string
	Dirty                        bool
	CtxPct                       int
	Cost                         float64
}

// Row is one rendered transcript entry, mapped from the TUI's live state.
type Row struct {
	Kind    string // user | assistant | toolcall | toolresult | permission | system | error
	Text    string
	Tool    string
	Status  string // ok | error (toolresult)
	Detail  string
	Running bool // toolcall: still in flight (no result yet)
}

// Perm is an in-flight permission prompt awaiting a decision.
type Perm struct {
	Tool, Risk, Reason, Summary string
}

// AskUser is an in-flight ask_user questionnaire awaiting an answer: the focused
// question (one of Total, 0-based Index), its options, an optional header, and the
// answer typed so far. When set, RenderChat shows the questionnaire instead of the
// working/thinking spinner.
type AskUser struct {
	Header   string
	Question string
	Options  []string
	Index    int
	Total    int
	Input    string
}

// Suggestion is one slash-command autocomplete row threaded in from the TUI.
type Suggestion struct {
	Name string
	Desc string
}

// Picker is an open interactive selector overlay threaded in from the TUI: a
// title, the visible item labels, and the highlighted index.
type Picker struct {
	Title    string
	Items    []string
	Selected int
}

// HomeData drives the Zen home page.
type HomeData struct {
	Variant       int
	Dark          bool
	Width, Height int
	Header        Header
	Recent        [][3]string
	Input         string
	// Suggestions / SelectedIdx drive the slash-command autocomplete overlay; an
	// empty slice means no overlay. Picker, when non-nil, is an open selector.
	Suggestions []Suggestion
	SelectedIdx int
	Picker      *Picker
}

// ChatData drives the Statusline chat page.
type ChatData struct {
	Variant       int
	Dark          bool
	Width, Height int
	Header        Header
	Rows          []Row
	Working       bool
	Thinking      bool   // waiting for the model's first token
	Stream        string // live assistant text being streamed
	TokS          int    // streaming tokens/sec
	Spin          int
	Perm          *Perm
	// AskUser, when non-nil, is a pending ask_user questionnaire. It renders the
	// focused question (over the spinner) so the zeroline skin mirrors the default
	// skin instead of showing a misleading "working…".
	AskUser *AskUser
	Input   string
	// Suggestions / SelectedIdx drive the slash-command autocomplete overlay; an
	// empty slice means no overlay. Picker, when non-nil, is an open selector.
	Suggestions []Suggestion
	SelectedIdx int
	Picker      *Picker
}

type styles struct {
	pal             Pal
	variant         int  // theme index, for keying the markdown renderer cache
	dark            bool // light/dark mode, for keying the markdown renderer cache
	fg, dim, mute   lipgloss.Style
	acc, acc2       lipgloss.Style
	green, red, amb lipgloss.Style
}

// newStyles builds foreground-only text styles. These compose INSIDE the chat
// status bars (which set their own Panel backgrounds), so they must NOT bake in a
// background of their own. variant/dark identify the active theme for the
// markdown renderer cache.
func newStyles(p Pal, variant int, dark bool) styles {
	f := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	return styles{p, variant, dark, f(p.Fg), f(p.Dim), f(p.Mute), f(p.Accent), f(p.Accent2), f(p.Green), f(p.Red), f(p.Amber)}
}

// newCanvasStyles is for full-bleed surfaces (the home + boot splash) where text
// sits directly on the themed background. Each style carries the theme background
// so content cells match the surrounding whitespace fill — otherwise the text
// shows the terminal's own background, producing a visible "card" against the
// themed margins.
func newCanvasStyles(p Pal, variant int, dark bool) styles {
	f := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c).Background(p.Bg)
	}
	return styles{p, variant, dark, f(p.Fg), f(p.Dim), f(p.Mute), f(p.Accent), f(p.Accent2), f(p.Green), f(p.Red), f(p.Amber)}
}

// block is a solid caret cell used for the streaming cursor.
func (s styles) block() string {
	return lipgloss.NewStyle().Background(s.pal.Accent).Render(" ")
}

// RenderBoot renders the launch splash: the ZERO wordmark reveals line-by-line,
// then the tagline and a loading line, advancing by animation frame (~120ms).
func RenderBoot(variant int, dark bool, frame, w, h int) string {
	p := Resolve(variant, dark)
	s := newCanvasStyles(p, variant, dark)
	reveal := []int{1, 3, 5, 7, 9} // per-line reveal frames (~120ms each)
	var b strings.Builder
	for i, l := range wordmark {
		if i < len(reveal) && frame >= reveal[i] {
			b.WriteString(s.acc.Render(l) + "\n")
		} else {
			b.WriteString(strings.Repeat(" ", len([]rune(l))) + "\n")
		}
	}
	b.WriteString("\n")
	if frame >= 11 {
		b.WriteString(s.dim.Render("Own your agent. ") + s.acc2.Render("Any model.") + s.dim.Render(" Zero lock-in.") + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if frame >= 8 {
		b.WriteString(s.mute.Render("initializing runtime · loading providers ") + s.amb.Render(spinFrames[frame%len(spinFrames)]))
	}
	content := lipgloss.NewStyle().Align(lipgloss.Center).Background(p.Bg).Render(b.String())
	return lipgloss.Place(maxi(w, 40), maxi(h, 8), lipgloss.Center, lipgloss.Center, content,
		lipgloss.WithWhitespaceBackground(p.Bg))
}

// ---------------------------------------------------------------- HOME (ZEN)

// RenderHome renders the centered Zen landing surface.
func RenderHome(d HomeData) string {
	p := Resolve(d.Variant, d.Dark)
	s := newCanvasStyles(p, d.Variant, d.Dark)
	w := maxi(d.Width, 40)

	var b strings.Builder
	for _, l := range wordmark {
		b.WriteString(s.acc.Render(l) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(s.dim.Render("Own your agent. ") + s.acc2.Render("Any model.") + s.dim.Render(" Zero lock-in.") + "\n\n")
	b.WriteString(headerStripe(s, d.Header) + "\n\n")

	if len(d.Recent) > 0 {
		b.WriteString(s.mute.Render("recent sessions") + "\n")
		for _, r := range d.Recent {
			title := r[0]
			pad := 26 - len(title)
			if pad < 1 {
				pad = 1
			}
			b.WriteString(s.mute.Render("› ") + s.fg.Render(title) + strings.Repeat(" ", pad) +
				s.dim.Render(r[1]) + "  " + s.mute.Render(r[2]) + "\n")
		}
		b.WriteString("\n")
	}

	box := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(p.Line).
		BorderBackground(p.Bg).Background(p.Bg).
		Padding(0, 1).Width(mini(58, w-4)).Render(d.Input)
	b.WriteString(box + "\n")
	homeOverlayCap := len(d.Suggestions) + 1
	if d.Picker != nil {
		homeOverlayCap = len(d.Picker.Items) + 1
	}
	if overlay := s.overlayRegion(ChatData{Suggestions: d.Suggestions, SelectedIdx: d.SelectedIdx, Picker: d.Picker}, mini(58, w-4), homeOverlayCap); overlay != "" {
		b.WriteString(overlay + "\n")
	}
	b.WriteString("\n")
	b.WriteString(s.mute.Render("⏎ start · 1-5 theme · ^L light · / commands · @ files · ! bash · ^C quit"))

	content := lipgloss.NewStyle().Align(lipgloss.Center).Background(p.Bg).Render(b.String())
	return lipgloss.Place(w, maxi(d.Height, 8), lipgloss.Center, lipgloss.Center, content,
		lipgloss.WithWhitespaceBackground(p.Bg))
}

func headerStripe(s styles, h Header) string {
	dirty := ""
	if h.Dirty {
		dirty = s.amb.Render("✱")
	}
	parts := s.dim.Render(shortPath(h.Cwd)) + s.dim.Render(" · ⎇ ") + s.dim.Render(orDash(h.Branch)) + dirty +
		s.dim.Render(" · ") + s.fg.Render(orDash(h.Model)) + s.dim.Render(" · ") + s.dim.Render(orDash(h.Provider))
	return parts
}

// ------------------------------------------------------------- CHAT (STATUS)

// RenderChat renders the Statusline chat surface from live agent state.
func RenderChat(d ChatData) string {
	p := Resolve(d.Variant, d.Dark)
	s := newStyles(p, d.Variant, d.Dark)
	w := maxi(d.Width, 40)
	h := maxi(d.Height, 8)

	run := "normal"
	switch {
	case d.Perm != nil, d.AskUser != nil:
		run = "blocked"
	case d.Working:
		run = "work"
	case hasAssistant(d.Rows):
		run = "done"
	}

	top := s.topBar(run, d.Header, w)
	bottom := s.botBar(run, d.Header, d.Variant, d.TokS, w)
	cmd := s.cmdRegion(d, w)

	// The autocomplete / picker overlay (when present) sits between the command
	// line and the bottom bar; its lines are subtracted from the transcript body
	// so the frame keeps its fixed height. Cap the overlay so it can never push
	// the body below one row (top + cmd + bottom = 3 fixed rows, plus >=1 body),
	// which would otherwise overflow the frame's allotted height.
	maxOverlay := h - 3 - 1
	if maxOverlay < 0 {
		maxOverlay = 0
	}
	overlay := s.overlayRegion(d, w, maxOverlay)
	overlayH := 0
	if overlay != "" {
		overlayH = strings.Count(overlay, "\n") + 1
	}

	bodyH := h - 3 - overlayH
	if bodyH < 1 {
		bodyH = 1
	}
	var body string
	if d.Perm != nil {
		body = s.permModal(d.Perm, w, bodyH)
	} else {
		body = s.transcript(d, w, bodyH)
	}
	frame := top + "\n" + body + "\n" + cmd
	if overlay != "" {
		frame += "\n" + overlay
	}
	frame += "\n" + bottom
	// Frame-safety: guarantee no rendered line exceeds the frame width, so chrome
	// (top/bottom bars) and the modal/body stay inside the terminal even at narrow
	// widths. clip is width-aware + ANSI-safe and a no-op when a line already fits.
	return clampFrameWidth(frame, w)
}

// clampFrameWidth clips every line of the composed frame to w display cells so no
// row can overflow the terminal (a backstop over the per-component clipping).
func clampFrameWidth(frame string, w int) string {
	lines := strings.Split(frame, "\n")
	for i, ln := range lines {
		if lipgloss.Width(ln) > w {
			lines[i] = clip(ln, w)
		}
	}
	return strings.Join(lines, "\n")
}

// overlayRegion renders the slash-command autocomplete list or an open picker on
// the theme background, just below the command line. Returns "" when neither is
// present. A picker takes precedence over the suggestion list. maxRows caps the
// number of rendered rows so the overlay can never overflow the frame; a
// non-positive maxRows suppresses the overlay entirely.
//
// While a blocking surface is up the overlay is suppressed. For a permission
// prompt, PermLayout (the mouse hit-test) lays the modal out assuming no overlay
// rows, so showing one would drift the rendered buttons away from their
// hitboxes. For an ask_user questionnaire, RenderChat treats it as the focused
// blocking surface, so overlay rows would consume bodyH and push the question
// offscreen.
func (s styles) overlayRegion(d ChatData, w, maxRows int) string {
	if d.Perm != nil || d.AskUser != nil || maxRows <= 0 {
		return ""
	}
	if d.Picker != nil {
		return s.pickerLines(*d.Picker, w, maxRows)
	}
	if len(d.Suggestions) > 0 {
		return s.suggestionLines(d.Suggestions, d.SelectedIdx, w, maxRows)
	}
	return ""
}

// suggestionLines renders one row per match (name + dim description) on the
// theme background; the selected row is highlighted with a caret and accent.
// maxRows caps the rows (including a trailing "… N more" row when truncated) so
// the overlay never overflows the frame.
func (s styles) suggestionLines(items []Suggestion, selected, w, maxRows int) string {
	nameW := 0
	for _, it := range items {
		if l := lipgloss.Width(it.Name); l > nameW {
			nameW = l
		}
	}
	visible, hidden := capRows(len(items), maxRows)
	lines := make([]string, 0, visible+1)
	for i := 0; i < visible; i++ {
		it := items[i]
		pad := strings.Repeat(" ", maxi(0, nameW-lipgloss.Width(it.Name)))
		marker := s.mute.Render("  ")
		name := s.fg.Render(it.Name)
		if i == selected {
			marker = s.acc.Bold(true).Render("› ")
			name = s.acc.Bold(true).Render(it.Name)
		}
		line := marker + name + pad + s.dim.Render("  "+it.Desc)
		lines = append(lines, padRight(clip(line, w), w, s.pal.Bg))
	}
	if hidden > 0 {
		lines = append(lines, padRight(clip(s.mute.Render(fmt.Sprintf("  … %d more", hidden)), w), w, s.pal.Bg))
	}
	return strings.Join(lines, "\n")
}

// pickerLines renders an open selector: a title line plus one row per item, the
// selected row highlighted, all on the theme background. maxRows caps the total
// rows (title + items + an optional "… N more") so the overlay fits the frame.
func (s styles) pickerLines(p Picker, w, maxRows int) string {
	lines := make([]string, 0, len(p.Items)+1)
	head := s.acc.Bold(true).Render(p.Title) + s.mute.Render("  ↑/↓ move · ⏎ select · esc cancel")
	lines = append(lines, padRight(clip(head, w), w, s.pal.Bg))
	// The title consumes one row; the rest are available for items.
	visible, hidden := capRows(len(p.Items), maxRows-1)
	for i := 0; i < visible; i++ {
		item := p.Items[i]
		marker := s.mute.Render("  ")
		label := s.fg.Render(item)
		if i == p.Selected {
			marker = s.acc.Bold(true).Render("› ")
			label = s.acc.Bold(true).Render(item)
		}
		lines = append(lines, padRight(clip(marker+label, w), w, s.pal.Bg))
	}
	if hidden > 0 {
		lines = append(lines, padRight(clip(s.mute.Render(fmt.Sprintf("  … %d more", hidden)), w), w, s.pal.Bg))
	}
	return strings.Join(lines, "\n")
}

// capRows returns how many of total rows to render given a maxRows budget, and
// how many are hidden. When everything fits, hidden is 0. When it doesn't, one
// row is reserved for a "… N more" summary so the visible count leaves room for
// it (visible + 1 summary <= maxRows). A non-positive budget shows nothing.
func capRows(total, maxRows int) (visible, hidden int) {
	if maxRows <= 0 {
		return 0, total
	}
	if total <= maxRows {
		return total, 0
	}
	visible = maxRows - 1 // reserve one row for the "… N more" summary
	if visible < 0 {
		visible = 0
	}
	return visible, total - visible
}

// Rect is a screen region in cell coordinates (0-based, y measured from the top
// of the whole frame including the top status bar).
type Rect struct{ X, Y, W, H int }

// PermGeometry holds the clickable button regions of the centered permission
// modal. It is computed purely from width/height so the renderer and the mouse
// hit-test always agree.
type PermGeometry struct {
	Active              bool
	Allow, Always, Deny Rect
}

// Hit returns "allow", "always", "deny" or "" for a click at (x, y).
func (g PermGeometry) Hit(x, y int) string {
	in := func(r Rect) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }
	switch {
	case in(g.Allow):
		return "allow"
	case in(g.Always):
		return "always"
	case in(g.Deny):
		return "deny"
	}
	return ""
}

// permModalRows is the number of lines permModalLines emits and permBtnRow is the
// 0-based index of the buttons line within it. PermLayout uses these to place the
// hitboxes exactly where permModal centers the modal; permModalLines asserts the
// count at construction so the two can never drift.
const (
	permModalRows = 8
	permBtnRow    = 5
	// permMinBoxWidth is the smallest box width that fits the three buttons on one
	// row inside the modal frame: 43 button-row cells ("[ a · allow ]" 13 + 2 gap
	// + "[ y · always ]" 14 + 2 gap + "[ d · deny ]" 12) plus a 1-cell border and
	// 1-cell padding on each side. Below this the button row overflows the frame
	// (and can clip at the terminal edge), so PermLayout disables the hitboxes.
	permMinBoxWidth = 47
)

func permBoxWidth(w int) int {
	bw := 52
	if bw > w-2 {
		bw = w - 2
	}
	if bw < 38 {
		bw = 38
	}
	return bw
}

// PermLayout computes the button hitboxes for the centered modal. Must stay in
// lockstep with permModal/permModalLines below, which means mirroring the exact
// clamps RenderChat applies before laying out the body: width floored to 40,
// height to 8, and bodyH = height-3 (floored to 1) with no overlay rows present
// when a permission prompt is up.
func PermLayout(width, height int) PermGeometry {
	width = maxi(width, 40)
	height = maxi(height, 8)
	bw := permBoxWidth(width)
	// Too narrow to render the three buttons inside the modal frame: the button
	// row would overflow the box border and can clip at the terminal edge, so the
	// rendered buttons no longer reliably match these hitboxes. Disable them (the
	// keyboard shortcuts still work) — mirrors the vertical-clip guard below.
	// (A vertically-stacked narrow layout is a future enhancement.)
	if bw < permMinBoxWidth {
		return PermGeometry{Active: false}
	}
	bodyH := height - 3
	if bodyH < 1 {
		bodyH = 1
	}
	top := (bodyH - permModalRows) / 2 // permModal centers a permModalRows-line modal in bodyH
	if top < 0 {
		top = 0
	}
	// If the body is too short to actually render the button row, the modal is
	// clipped above the buttons — disable the hitboxes so a click can't resolve
	// to allow/deny on a row where no button is drawn.
	if top+permBtnRow >= bodyH {
		return PermGeometry{Active: false}
	}
	bx := (width - bw) / 2
	if bx < 0 {
		bx = 0
	}
	btnY := 1 + top + permBtnRow // top bar row + modal top + buttons row index
	return PermGeometry{
		Active: true,
		Allow:  Rect{bx + 2, btnY, 13, 1},
		Always: Rect{bx + 17, btnY, 14, 1},
		Deny:   Rect{bx + 33, btnY, 12, 1},
	}
}

func (s styles) topBar(run string, h Header, w int) string {
	p := s.pal
	var modeTxt string
	var modeBg lipgloss.Color
	switch run {
	case "work":
		modeTxt, modeBg = "⟳ WORKING", p.Amber
	case "done":
		modeTxt, modeBg = "✓ DONE", p.Green
	case "blocked":
		modeTxt, modeBg = "⚠ BLOCKED", p.Red
	default:
		modeTxt, modeBg = "NORMAL", p.Accent
	}
	mode := lipgloss.NewStyle().Background(modeBg).Foreground(p.Bg).Bold(true).Padding(0, 1).Render(modeTxt)
	b1 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel2).Padding(0, 1).Render(in) }
	b2 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel).Padding(0, 1).Render(in) }

	dirty := ""
	if h.Dirty {
		dirty = s.amb.Render("✱")
	}
	branch := b1(s.fg.Render("⎇ "+orDash(h.Branch)) + dirty)
	cwd := b2(s.dim.Render(shortPath(h.Cwd)))
	model := b2(s.mute.Render("model ") + s.fg.Render(orDash(h.Model)))
	prov := b2(s.mute.Render("prov ") + s.dim.Render(orDash(h.Provider)))
	ctx := b2(s.mute.Render("ctx ") + s.fg.Render(strconv.Itoa(h.CtxPct)+"%"))
	cost := b1(s.mute.Render("$ ") + s.fg.Render(fmt.Sprintf("%.2f", h.Cost)))

	return bar(mode+branch+cwd+model, prov+ctx+cost, w, p.Panel)
}

func (s styles) botBar(run string, h Header, variant, tokS, w int) string {
	p := s.pal
	caution := run == "blocked"
	stTxt := map[string]string{"work": "WORKING", "done": "DONE", "blocked": "BLOCKED", "normal": "READY"}[run]
	dot := lipgloss.NewStyle().Background(p.Accent).Render(" ")
	stStyle := s.fg
	if caution {
		stStyle = s.red.Bold(true)
	}
	b1 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel2).Padding(0, 1).Render(in) }
	b2 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel).Padding(0, 1).Render(in) }

	tps := s.green.Render("0")
	if tokS > 0 {
		tps = s.green.Render(strconv.Itoa(tokS))
	}
	left := b1(dot+" "+stStyle.Render(stTxt)) + b2(s.dim.Render("utf-8")) +
		b2(s.mute.Render("tok/s ")+tps)
	right := b2(s.mute.Render("ctx ")+s.gauge(float64(h.CtxPct)/100, 8)) +
		b2(s.mute.Render(ThemeName(variant))) +
		b1(s.mute.Render("1-5 theme · ^L light"))
	return bar(left, right, w, p.Panel)
}

func (s styles) gauge(v float64, w int) string {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	f := int(v*float64(w) + 0.5)
	if f > w {
		f = w
	}
	return s.mute.Render("▕") + s.acc.Render(strings.Repeat("█", f)) +
		lipgloss.NewStyle().Foreground(s.pal.Line2).Render(strings.Repeat("░", w-f)) + s.mute.Render("▏")
}

func bar(left, right string, w int, fill lipgloss.Color) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	spacer := lipgloss.NewStyle().Background(fill).Render(strings.Repeat(" ", gap))
	return left + spacer + right
}

func (s styles) cmdRegion(d ChatData, w int) string {
	p := s.pal
	if d.Perm != nil {
		var hint string
		if PermLayout(w, d.Height).Active {
			hint = s.mute.Render("click a choice · keys ") +
				s.acc.Render("a") + s.mute.Render("/") + s.acc.Render("y") + s.mute.Render("/") + s.acc.Render("d") +
				s.mute.Render(" · Esc cancel")
		} else {
			hint = s.mute.Render("keys ") +
				s.acc.Render("a") + s.mute.Render("/") + s.acc.Render("y") + s.mute.Render("/") + s.acc.Render("d") +
				s.mute.Render(" · Esc cancel")
		}
		return padRight(hint, w, p.Bg)
	}
	line := s.acc.Bold(true).Render(":") + " " + d.Input
	return padRight(line, w, p.Bg)
}

// permModal renders the centered permission modal across the body region.
func (s styles) permModal(p *Perm, w, bodyH int) string {
	bw := permBoxWidth(w)
	modal := s.permModalLines(p, bw)
	top := (bodyH - len(modal)) / 2
	if top < 0 {
		top = 0
	}
	left := (w - bw) / 2
	if left < 0 {
		left = 0
	}
	bg := func(n int) string {
		if n < 0 {
			n = 0
		}
		return lipgloss.NewStyle().Background(s.pal.Bg).Render(strings.Repeat(" ", n))
	}
	blank := bg(w)
	out := make([]string, 0, bodyH)
	for i := 0; i < bodyH; i++ {
		mi := i - top
		if mi >= 0 && mi < len(modal) {
			out = append(out, bg(left)+modal[mi]+bg(w-left-bw))
		} else {
			out = append(out, blank)
		}
	}
	return strings.Join(out, "\n")
}

func (s styles) permModalLines(p *Perm, bw int) []string {
	amber := lipgloss.NewStyle().Foreground(s.pal.Amber)
	vb := amber.Render("│")
	contentW := bw - 4 // borders + one space of padding each side

	title := "permission required"
	dashN := bw - 3 - lipgloss.Width(title) - 2
	if dashN < 0 {
		dashN = 0
	}
	topLine := amber.Render("╭─ ") + s.amb.Bold(true).Render(title) + amber.Render(" "+strings.Repeat("─", dashN)+"╮")
	botLine := amber.Render("╰" + strings.Repeat("─", bw-2) + "╯")

	content := func(c string) string {
		pad := contentW - lipgloss.Width(c)
		if pad < 0 {
			pad = 0
		}
		return vb + " " + c + strings.Repeat(" ", pad) + " " + vb
	}

	toolLine := s.fg.Bold(true).Render(clip(p.Tool, contentW))
	risk := ""
	if p.Risk != "" {
		risk = "RISK " + strings.ToUpper(p.Risk)
	}
	reason := clip(orDash(p.Reason), contentW-lipgloss.Width(risk)-2)
	meta := s.dim.Render(reason)
	if risk != "" {
		meta += s.dim.Render("  ") + s.amb.Render(risk)
	}

	allowBtn := lipgloss.NewStyle().Background(s.pal.Accent).Foreground(s.pal.Bg).Bold(true).Render("[ a · allow ]")
	alwaysBtn := s.dim.Render("[ ") + s.acc.Render("y") + s.dim.Render(" · always ]")
	denyBtn := s.dim.Render("[ ") + s.acc.Render("d") + s.dim.Render(" · deny ]")
	buttons := allowBtn + "  " + alwaysBtn + "  " + denyBtn
	if bw < permMinBoxWidth {
		// Too narrow for the full button row (it would overflow the modal frame);
		// render a compact keyboard-only hint clipped to the content width instead.
		// PermLayout disables the mouse hitboxes at this width, so the rendered row
		// and the (absent) hit-test stay aligned.
		buttons = clip(s.acc.Render("a")+s.dim.Render(" allow  ")+s.acc.Render("y")+s.dim.Render(" always  ")+s.acc.Render("d")+s.dim.Render(" deny"), contentW)
	}

	lines := []string{
		topLine,
		content(""),
		content(toolLine),
		content(meta),
		content(""),
		content(buttons), // index permBtnRow
		content(""),
		botLine,
	}
	// Guard: PermLayout places the button hitboxes assuming this exact shape, so
	// keep the count and the buttons row in lockstep with the constants.
	if len(lines) != permModalRows {
		panic("permModalLines: modal line count drifted from permModalRows")
	}
	return lines
}

// askUserLines renders the focused ask_user question as a "✦ zero" block: an
// optional header, the "question N of M" counter, the question, its options, the
// answer typed so far, and a short hint. Mirrors the default skin's focused
// questionnaire so the zeroline surface no longer shows a misleading spinner.
func (s styles) askUserLines(a *AskUser, tw int) []string {
	heading := s.acc2.Bold(true).Render("✦ ask zero")
	if header := strings.TrimSpace(a.Header); header != "" {
		heading += "  " + s.fg.Render(clip(header, tw-12))
	}
	lines := []string{heading}
	if a.Total > 0 {
		lines = append(lines, "        "+s.dim.Render(fmt.Sprintf("question %d of %d", a.Index+1, a.Total)))
	}
	lines = append(lines, "        "+s.fg.Render(clip(a.Question, tw-9)))
	if len(a.Options) > 0 {
		lines = append(lines, "        "+s.dim.Render(clip("options: "+strings.Join(a.Options, ", "), tw-9)))
	}
	answer := strings.TrimSpace(a.Input)
	if answer == "" {
		answer = "—"
	}
	lines = append(lines, "        "+s.mute.Render("› ")+s.fg.Render(clip(answer, tw-11)))
	lines = append(lines, "        "+s.dim.Render("type an answer, Enter to submit · Esc to skip"))
	return lines
}

func (s styles) transcript(d ChatData, w, h int) string {
	tw := w - 4
	var lines []string
	add := func(ls ...string) { lines = append(lines, ls...) }
	blank := func() {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
	}

	for _, r := range d.Rows {
		switch r.Kind {
		case "user":
			blank()
			add(s.mute.Render("› ") + s.acc.Bold(true).Render("you ") + s.fg.Render(clip(r.Text, tw-6)))
		case "assistant":
			blank()
			add(s.acc2.Bold(true).Render("✦ zero"))
			lines = append(lines, s.renderAssistant(r.Text, tw, true)...)
		case "toolcall":
			blank()
			marker := s.mute.Render("▸")
			if r.Running {
				marker = s.amb.Render(spinFrames[d.Spin%len(spinFrames)])
			}
			line := marker + " " + toolIcon(s, r.Tool) + " " + s.acc2.Render(toolLabel(r.Tool))
			if a := clip(firstLine(r.Detail), tw-22); a != "" {
				line += "  " + s.dim.Render(a)
			}
			add(line)
		case "toolresult":
			summary, showBody, bodyMax := resultSummary(r.Tool, r.Status, r.Detail)
			if r.Status == "error" {
				add("  " + s.mute.Render("⎿ ") + s.red.Render(clip(firstLine(r.Detail), tw-8)))
			} else if summary != "" {
				add("  " + s.mute.Render("⎿ ") + s.dim.Render(clip(summary, tw-8)))
			}
			if showBody && r.Status != "error" {
				if (r.Tool == "edit_file" || r.Tool == "apply_patch") && looksLikeDiff(r.Detail) {
					lines = append(lines, s.colorizeDiff(r.Detail, tw)...)
				} else {
					lines = append(lines, s.renderCodeBlock(r.Detail, tw, bodyMax)...)
				}
			}
		case "permission":
			blank()
			add(s.amb.Render("⚠ ") + s.dim.Render(clip(r.Text, tw-4)))
		case "system":
			blank()
			for _, dl := range strings.Split(r.Text, "\n") {
				add(s.dim.Render(clip(dl, tw)))
			}
		case "error":
			blank()
			add(s.red.Render("✗ " + clip(r.Text, tw-4)))
		}
	}

	switch {
	case d.AskUser != nil:
		// A pending questionnaire takes the place of the thinking/streaming line:
		// show the focused question, not a misleading spinner.
		blank()
		add(s.askUserLines(d.AskUser, tw)...)
	case d.Stream != "":
		blank()
		add(s.acc2.Bold(true).Render("✦ zero"))
		slines := s.renderAssistant(d.Stream, tw, false)
		if len(slines) > 0 {
			slines[len(slines)-1] += s.block() // streaming caret
		}
		lines = append(lines, slines...)
	case d.Thinking:
		blank()
		add(s.acc2.Bold(true).Render("✦ zero") + "  " + s.amb.Render(spinFrames[d.Spin%len(spinFrames)]) +
			s.dim.Render(" thinking"+strings.Repeat(".", d.Spin%4)))
	}

	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}

	out := strings.Join(lines, "\n")
	if d.Perm != nil {
		out = lipgloss.NewStyle().Faint(true).Render(out)
	}
	return lipgloss.NewStyle().PaddingLeft(2).Render(out)
}

// renderAssistant lays out a model message. Completed messages (markdown=true)
// are rendered through glamour for full CommonMark + chroma-highlighted fenced
// code in one pass; live streaming text (markdown=false) stays plain because
// partial markdown renders badly mid-stream. Either way the body is indented to
// sit under the "✦ zero" label.
func (s styles) renderAssistant(text string, tw int, markdown bool) []string {
	if markdown && strings.TrimSpace(text) != "" {
		return s.renderAssistantMarkdown(text, tw)
	}
	return s.renderAssistantPlain(text, tw)
}

// renderAssistantMarkdown runs the message through glamour and lays the result
// out under the label with the same 8-space indent as the plain path, on the
// theme background so no card reappears against the full-bleed canvas.
func (s styles) renderAssistantMarkdown(text string, tw int) []string {
	wrapW := tw - 9
	if wrapW < 8 {
		wrapW = 8
	}
	md := renderMarkdown(text, s.pal, s.variant, s.dark, wrapW)
	if strings.TrimSpace(md) == "" {
		return s.renderAssistantPlain(text, tw)
	}
	// glamour word-wraps at wrapW but does NOT hard-break unbreakable tokens (a
	// long URL or fenced-code line), so it can emit a line far wider than the
	// frame. Clip each output line to the budget (tw-8, the width left after the
	// 8-space indent) with an ANSI-aware truncate so styled escapes stay intact.
	clipW := tw - 8
	if clipW < 1 {
		clipW = 1
	}
	bg := lipgloss.NewStyle().Background(s.pal.Bg)
	var out []string
	for _, ln := range strings.Split(md, "\n") {
		out = append(out, bg.Render("        ")+ansi.Truncate(ln, clipW, "…"))
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// renderAssistantPlain word-wraps prose; fenced code blocks are kept verbatim in
// an aligned, clipped block with a gutter so code never re-wraps or knocks the
// layout out of alignment. Used for live streaming text.
func (s styles) renderAssistantPlain(text string, tw int) []string {
	var out []string
	inCode := false
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			inCode = !inCode
			out = append(out, "        "+s.mute.Render("┄┄┄┄┄┄"))
			continue
		}
		if inCode {
			out = append(out, "        "+s.mute.Render("│ ")+s.fg.Render(clip(detab(ln), tw-12)))
			continue
		}
		if t == "" {
			out = append(out, "")
			continue
		}
		for _, wl := range wrap(t, tw-9) {
			// clip the wrapped line too: wrap splits on spaces and does not break a
			// single word longer than the budget, so a long URL-like token or an
			// unbroken CJK run would otherwise overflow the frame.
			out = append(out, "        "+s.fg.Render(clip(wl, tw-9)))
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// renderCodeBlock renders tool output (file contents, diffs, listings) as an
// aligned block with a left gutter, clipped to width and capped at max lines.
// Unified diffs get +/-/@@ coloring; everything else stays neutral.
func (s styles) renderCodeBlock(detail string, tw, max int) []string {
	detail = strings.TrimRight(detail, "\n")
	if detail == "" {
		return nil
	}
	isDiff := strings.Contains(detail, "@@ ") || strings.HasPrefix(strings.TrimSpace(detail), "diff ") ||
		strings.Contains(detail, "\n--- ") || strings.Contains(detail, "\n+++ ")
	lines := strings.Split(detail, "\n")
	gut := s.mute.Render("│ ")
	var out []string
	for i, ln := range lines {
		if i >= max {
			out = append(out, "      "+s.mute.Render(fmt.Sprintf("│ … +%d more lines", len(lines)-i)))
			break
		}
		out = append(out, "      "+gut+s.codeLine(detab(ln), tw-8, isDiff))
	}
	return out
}

func (s styles) codeLine(ln string, w int, isDiff bool) string {
	c := clip(ln, w)
	if isDiff {
		switch {
		case strings.HasPrefix(ln, "@@"):
			return s.acc.Render(c)
		case strings.HasPrefix(ln, "+"):
			return s.green.Render(c)
		case strings.HasPrefix(ln, "-"):
			return s.red.Render(c)
		}
	}
	return s.dim.Render(c)
}

// diffMaxLines caps how many diff lines are colorized inline before a "… N more
// lines" footer takes over, so a huge patch can't blow out the transcript.
const diffMaxLines = 40

// looksLikeDiff reports whether detail is a unified diff (hunk headers, file
// markers, or +/- body lines), the trigger for structured diff rendering.
func looksLikeDiff(detail string) bool {
	if strings.Contains(detail, "@@") {
		return true
	}
	for _, ln := range strings.Split(detail, "\n") {
		switch {
		case strings.HasPrefix(ln, "+++ "), strings.HasPrefix(ln, "--- "):
			return true
		case strings.HasPrefix(ln, "+"), strings.HasPrefix(ln, "-"):
			return true
		}
	}
	return false
}

// colorizeDiff is the standalone, dependency-free unified-diff colorizer with
// the requested (detail, Pal) signature: green additions, red deletions, dim
// context, mute hunk/file headers, a subtle left gutter, capped at diffMaxLines
// with a "… N more lines" footer. Returns a single joined string.
func colorizeDiff(detail string, p Pal) string {
	return strings.Join(newStyles(p, 0, true).colorizeDiff(detail, 0), "\n")
}

// colorizeDiff renders a unified diff with a subtle left gutter and per-line
// coloring: green for additions, red for deletions, mute for hunk/file headers,
// dim for context. Long diffs are capped at diffMaxLines with a "… N more lines"
// footer. tw is the available width (0 = no clipping); content sits indented to
// match the surrounding transcript.
func (s styles) colorizeDiff(detail string, tw int) []string {
	detail = strings.TrimRight(detail, "\n")
	if detail == "" {
		return nil
	}
	lines := strings.Split(detail, "\n")
	gut := s.mute.Render("│ ")
	cw := tw - 8
	var out []string
	for i, ln := range lines {
		if i >= diffMaxLines {
			out = append(out, "      "+s.mute.Render(fmt.Sprintf("│ … %d more lines", len(lines)-i)))
			break
		}
		out = append(out, "      "+gut+s.diffLine(detab(ln), cw))
	}
	return out
}

// diffLine colors one unified-diff line by its leading marker. cw <= 0 disables
// clipping (used by the standalone helper and tests).
func (s styles) diffLine(ln string, cw int) string {
	c := ln
	if cw > 0 {
		c = clip(ln, cw)
	}
	switch {
	case strings.HasPrefix(ln, "@@"),
		strings.HasPrefix(ln, "+++ "), strings.HasPrefix(ln, "--- "),
		strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "):
		return s.mute.Render(c)
	case strings.HasPrefix(ln, "+"):
		return s.green.Render(c)
	case strings.HasPrefix(ln, "-"):
		return s.red.Render(c)
	default:
		return s.dim.Render(c)
	}
}

func detab(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// resultSummary collapses a tool's raw output into a one-line summary the way a
// proper coding agent does (file reads → line counts, listings → entry counts),
// and decides whether the full body (diffs, shell output, grep hits) is shown.
func resultSummary(tool, status, detail string) (summary string, showBody bool, bodyMax int) {
	if status == "error" {
		return "", true, 3
	}
	switch tool {
	case "read_file":
		fl := firstLine(detail)
		if i := strings.LastIndex(fl, "("); i >= 0 {
			return strings.TrimSuffix(fl[i+1:], ")"), false, 0 // "N lines" / "lines a-b of N"
		}
		return "read", false, 0
	case "list_directory":
		if strings.HasPrefix(firstLine(detail), "Directory is empty") {
			return "empty", false, 0
		}
		return plural(countBodyLines(detail), "entry", "entries"), false, 0
	case "glob":
		return plural(countNonEmpty(detail), "match", "matches"), false, 0
	case "grep":
		fl := firstLine(detail)
		if strings.HasPrefix(fl, "0 matches") || strings.HasPrefix(fl, "No matches") {
			return "0 matches", false, 0
		}
		return plural(countNonEmpty(detail), "match", "matches"), true, 4
	case "write_file", "edit_file", "apply_patch":
		return "", true, 8
	case "bash":
		return "", true, 10
	default:
		return clip(firstLine(detail), 56), false, 0
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func countNonEmpty(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// countBodyLines counts non-empty lines after the first blank line (skips a
// header like "Contents of .:").
func countBodyLines(s string) int {
	parts := strings.SplitN(s, "\n\n", 2)
	if len(parts) == 2 {
		return countNonEmpty(parts[1])
	}
	return countNonEmpty(s)
}

// --- tool glyph mapping ------------------------------------------------------

func toolKind(tool string) string {
	switch tool {
	case "write_file", "edit_file", "apply_patch":
		return "write"
	case "bash":
		return "shell"
	case "update_plan":
		return "plan"
	default:
		return "read"
	}
}

func toolIcon(s styles, tool string) string {
	switch toolKind(tool) {
	case "write":
		return s.amb.Render("✎")
	case "shell":
		return s.amb.Render("❯")
	case "plan":
		return s.acc.Render("◷")
	default:
		return s.acc2.Render("◇")
	}
}

func toolLabel(tool string) string {
	if tool == "" {
		return "tool"
	}
	return tool
}

// --- small helpers -----------------------------------------------------------

var wordmark = []string{
	" ███████  ███████  ██████   ██████ ",
	"      ██  ██       ██   ██  ██   ██",
	"    ███   █████    ██████   ██   ██",
	"  ███     ██       ██   ██  ██   ██",
	" ███████  ███████  ██   ██   ██████",
}

func hasAssistant(rows []Row) bool {
	for _, r := range rows {
		if r.Kind == "assistant" {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func wrap(text string, w int) []string {
	if w < 8 {
		w = 8
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, word := range words {
		switch {
		case cur == "":
			cur = word
		case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// clip truncates s to a display width of w cells, appending an ellipsis when it
// overflows. It measures by terminal display width (not rune count) so wide
// runes (CJK, emoji) never exceed the budget, and it is ANSI-aware so styled
// escape sequences in s are preserved rather than counted/split.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func padRight(s string, w int, fill lipgloss.Color) string {
	gap := w - lipgloss.Width(s)
	if gap < 0 {
		gap = 0
	}
	return s + lipgloss.NewStyle().Background(fill).Render(strings.Repeat(" ", gap))
}

func shortPath(p string) string {
	if p == "" {
		return "~"
	}
	parts := strings.Split(p, "/")
	if len(parts) <= 3 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
