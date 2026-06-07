package zeroline

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	gstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// mdKey identifies a cached glamour renderer. A renderer bakes in the theme
// colors AND the word-wrap width, so any change to variant, mode or width needs
// a fresh one.
type mdKey struct {
	variant int
	dark    bool
	width   int
}

// mdOutKey caches the RENDERED output per message so glamour runs once per
// (message, theme, width) instead of every frame — RenderChat is called on every
// spinner tick and keystroke, so re-rendering all assistant markdown each time
// would make the TUI sluggish on a long transcript.
type mdOutKey struct {
	text    string
	variant int
	dark    bool
	width   int
}

const mdOutCacheMax = 512 // bounded so a resize drag (many widths) can't grow it forever
const mdCacheMax = 256    // ditto for the renderer cache: a resize mints a width key per frame

var (
	mdMu     sync.Mutex
	mdCache  = map[mdKey]*glamour.TermRenderer{}
	mdOutput = map[mdOutKey]string{}
)

// renderMarkdown renders CommonMark to styled terminal text using glamour, which
// also syntax-highlights fenced code blocks via chroma — markdown + code
// highlighting in a single pass. The output is themed to the active palette and
// sits on the canvas background so no "card" reappears against the full-bleed
// surface. Trailing blank lines glamour appends are trimmed so spacing stays
// tight with the surrounding transcript.
func renderMarkdown(text string, p Pal, variant int, dark bool, width int) string {
	if width < 1 {
		width = 1
	}
	key := mdOutKey{text, variant, dark, width}

	mdMu.Lock()
	if cached, ok := mdOutput[key]; ok {
		mdMu.Unlock()
		return cached
	}
	mdMu.Unlock()

	// markdownRenderer takes mdMu itself, so it must be called WITHOUT the lock
	// held (sync.Mutex is not reentrant).
	r := markdownRenderer(p, variant, dark, width)
	if r == nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	out = strings.Trim(out, "\n")

	mdMu.Lock()
	if len(mdOutput) >= mdOutCacheMax {
		mdOutput = map[mdOutKey]string{} // simple bounded reset; cheap to repopulate
	}
	mdOutput[key] = out
	mdMu.Unlock()
	return out
}

// markdownRenderer returns a glamour renderer for the given palette + width,
// building and caching one on first use. Cache misses build a renderer keyed by
// variant/mode/width so width changes (resizes) get a fresh, correctly wrapped
// renderer.
func markdownRenderer(p Pal, variant int, dark bool, width int) *glamour.TermRenderer {
	if width < 1 {
		width = 1
	}
	key := mdKey{variant, dark, width}

	mdMu.Lock()
	defer mdMu.Unlock()
	if r, ok := mdCache[key]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(themeStyleConfig(p, dark)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	if len(mdCache) >= mdCacheMax {
		mdCache = map[mdKey]*glamour.TermRenderer{} // simple bounded reset; cheap to repopulate
	}
	mdCache[key] = r
	return r
}

func sp(s string) *string { return &s }

// hex returns the lipgloss color as a "#rrggbb" string suitable for glamour /
// chroma color fields.
func hex(c lipgloss.Color) string { return string(c) }

// themeStyleConfig derives a glamour StyleConfig from the theme palette. It
// starts from glamour's stock dark/light style (so code-block chroma and list
// structure stay sensible) and overrides the load-bearing colors with the
// theme: headings -> Accent, links -> Accent2, inline code -> readable Fg on the
// panel bg, list bullets -> Mute, body text -> Fg on the theme Bg. The document
// background is set to the theme Bg to match the full-bleed canvas.
func themeStyleConfig(p Pal, dark bool) ansi.StyleConfig {
	var cfg ansi.StyleConfig
	if dark {
		cfg = gstyles.DarkStyleConfig
	} else {
		cfg = gstyles.LightStyleConfig
	}

	bg := hex(p.Bg)

	// Body text + full-bleed background, with no extra block margin so the
	// transcript's own indent governs alignment.
	cfg.Document.Color = sp(hex(p.Fg))
	cfg.Document.BackgroundColor = sp(bg)
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	zero := uint(0)
	cfg.Document.Margin = &zero

	// Headings -> Accent, bold, on the theme bg (drop glamour's H1 color block).
	cfg.Heading.Color = sp(hex(p.Accent))
	cfg.Heading.BackgroundColor = sp(bg)
	cfg.H1.Color = sp(hex(p.Accent))
	cfg.H1.BackgroundColor = sp(bg)
	cfg.H1.Bold = boolp(true)

	// Links + link text -> Accent2.
	cfg.Link.Color = sp(hex(p.Accent2))
	cfg.LinkText.Color = sp(hex(p.Accent2))

	// Inline code -> readable Fg on the panel background.
	cfg.Code.Color = sp(hex(p.Fg))
	cfg.Code.BackgroundColor = sp(hex(p.Panel))

	// List bullets / enumeration -> Mute.
	cfg.Item.Color = sp(hex(p.Mute))
	cfg.Enumeration.Color = sp(hex(p.Mute))

	// Block quotes + emphasis pick up theme dim/accent.
	cfg.BlockQuote.Color = sp(hex(p.Mute))
	cfg.Emph.Color = sp(hex(p.Dim))
	cfg.Strong.Color = sp(hex(p.Fg))

	return cfg
}

func boolp(b bool) *bool { return &b }
