package tui

import (
	"hash/fnv"
	"os"
	"strconv"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
)

// Glamour renders FINAL assistant answers as themed markdown — headings, lists,
// inline code, and syntax-highlighted fenced blocks — the key visual upgrade of
// the Lime Refined redesign. It is scoped to settled rows ONLY: the live token
// stream (interimBlock) and non-final rows keep the lightweight, partial-safe
// renderAssistantMarkdownText, and a completed turn re-renders through glamour
// once. glamour pulls its own (lipgloss v1) rendering stack, so we use it purely
// as a string→string markdown engine: its output never exchanges types with the
// v2 lipgloss tuiTheme — assistantGlamourLines hands back plain []string the
// transcript treats like any other rendered block.
//
// All token COLORS come from the active palette (zeroPalette), so no hex literal
// lives here and a /theme switch reaches glamour through clearGlamourCaches.

const (
	glamourRendererCacheMax = 8   // distinct widths kept (resize churn); reset past it
	glamourLineCacheMax     = 256 // distinct (width,text) renders kept; reset past it
)

type glamourRendererKey struct {
	gen   int
	width int
}

type glamourLines struct {
	display []string // glamour-rendered, ANSI-colored display lines
	plain   []string // ANSI-stripped copies, aligned 1:1 with display (selection/copy)
}

var (
	// glamourThemeGen increments on every palette swap so cached renderers/lines
	// built for the old palette are never reused.
	glamourThemeGen int

	glamourRendererCache = map[glamourRendererKey]*glamour.TermRenderer{}
	glamourLineCache     = map[string]glamourLines{}
)

// clearGlamourCaches invalidates the glamour renderer + line caches. Called from
// applyTheme (palette swap) on the update goroutine, alongside the render-cache
// clear, so the next render rebuilds glamour against the new palette.
func clearGlamourCaches() {
	glamourThemeGen++
	glamourRendererCache = map[glamourRendererKey]*glamour.TermRenderer{}
	glamourLineCache = map[string]glamourLines{}
}

// assistantGlamourLines renders text as Lime Refined markdown and returns the
// colored display lines plus their ANSI-stripped plain twins (same length). Both
// are derived from the SAME glamour pass so the transcript's selection/copy layer
// (which maps display line N to plain line N) stays aligned. Memoized by
// (theme generation, measure, text); falls back to the lightweight renderer if
// glamour is unavailable so a final row always renders something.
func assistantGlamourLines(text string, width int) ([]string, []string) {
	measure := assistantMeasure(width)
	key := glamourCacheKey(measure, text)
	if cached, ok := glamourLineCache[key]; ok {
		return cached.display, cached.plain
	}
	display, plain := renderAssistantGlamour(text, measure, width)
	if len(glamourLineCache) >= glamourLineCacheMax {
		glamourLineCache = map[string]glamourLines{}
	}
	glamourLineCache[key] = glamourLines{display: display, plain: plain}
	return display, plain
}

func renderAssistantGlamour(text string, measure, tableWidth int) ([]string, []string) {
	// Glamour truncates wide table cells with an ellipsis, dropping content; the
	// lightweight renderer wraps them and keeps every cell readable. So messages
	// containing a markdown table (and the glamour-unavailable fallback) render via
	// the lightweight path in the final ink tone — byte-identical to the pre-glamour
	// final row, which the rendering_lime table tests pin.
	if !markdownHasTable(text) {
		if renderer := glamourRendererFor(measure); renderer != nil {
			if out, err := renderer.Render(text); err == nil && strings.TrimSpace(xansi.Strip(out)) != "" {
				display := splitGlamourLines(out)
				plain := make([]string, len(display))
				for i, line := range display {
					plain[i] = xansi.Strip(line)
				}
				return display, plain
			}
		}
	}
	raw := renderAssistantMarkdownText(text, measure, tableWidth)
	display := make([]string, len(raw))
	plain := make([]string, len(raw))
	for i, line := range raw {
		display[i] = styleAssistantMarkdownLine(line, zeroTheme.ink)
		plain[i] = stripMarkdownRenderControls(line)
	}
	return display, plain
}

// markdownHasTable reports whether text contains a GitHub-flavored markdown table
// (a header row immediately followed by a |---|---| separator). Mirrors the table
// detection in renderAssistantMarkdownText so the hybrid renderer routes tables to
// the lightweight path. A false positive only means "render via the lightweight
// renderer," which is always safe.
func markdownHasTable(text string) bool {
	raw := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	for i := 0; i+1 < len(raw); i++ {
		if isMarkdownTableHeader(raw[i], raw[i+1]) {
			return true
		}
	}
	return false
}

// glamourRendererFor returns a cached glamour renderer for the active palette at
// the given wrap width. The renderer is the expensive part (goldmark + chroma
// setup), so it is built once per (palette, width) and reused every frame.
func glamourRendererFor(measure int) *glamour.TermRenderer {
	if measure < 16 {
		measure = 16
	}
	key := glamourRendererKey{gen: glamourThemeGen, width: measure}
	if renderer, ok := glamourRendererCache[key]; ok {
		return renderer
	}
	// Match the app's color policy: Ascii under NO_COLOR (any non-empty value) or
	// when stdout is not a TTY (tests), otherwise the environment's profile. This
	// keeps NO_COLOR honored for assistant markdown and tests deterministic/plain.
	profile := termenv.EnvColorProfile()
	if noColorRequested(os.Getenv) {
		profile = termenv.Ascii
	}
	base := glamourstyles.DarkStyleConfig
	if zeroPalette.bg == lightPalette.bg {
		base = glamourstyles.LightStyleConfig
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(glamourStyleConfig(base, zeroPalette)),
		glamour.WithWordWrap(measure),
		glamour.WithColorProfile(profile),
	)
	if err != nil {
		return nil
	}
	if len(glamourRendererCache) >= glamourRendererCacheMax {
		glamourRendererCache = map[glamourRendererKey]*glamour.TermRenderer{}
	}
	glamourRendererCache[key] = renderer
	return renderer
}

// glamourStyleConfig recolors a base glamour style with the Lime Refined palette:
// lime headings/links/bullets, white-bold strong text, a flush-left document, and
// the base chroma theme kept for fenced-code syntax highlighting. Only pointers
// are replaced — the shared base strings are never mutated.
func glamourStyleConfig(base glamouransi.StyleConfig, p palette) glamouransi.StyleConfig {
	s := base
	flush := uint(0)

	// Flush the document and code block left so prose aligns with the gutter.
	s.Document.Margin = &flush
	s.Document.BlockPrefix = ""
	s.Document.BlockSuffix = ""
	s.Document.Color = strPtr(p.ink)
	s.CodeBlock.Margin = &flush

	// Lime accents.
	s.Heading.Color = strPtr(p.accent)
	s.Heading.Bold = boolPtr(true)
	s.H1.Color = strPtr(p.accent)
	s.H1.BackgroundColor = nil // drop the base blue badge fill
	s.H1.Bold = boolPtr(true)
	s.Strong.Color = strPtr(p.white)
	s.Strong.Bold = boolPtr(true)
	s.HorizontalRule.Color = strPtr(p.faint)
	s.Item.Color = strPtr(p.accent)
	s.Enumeration.Color = strPtr(p.accent)
	s.Link.Color = strPtr(p.accent)
	s.Link.Underline = boolPtr(true)
	s.LinkText.Color = strPtr(p.accent)
	s.BlockQuote.Color = strPtr(p.muted)

	return s
}

// splitGlamourLines turns glamour's single rendered string into lines, dropping
// the leading/trailing blank lines glamour frames blocks with so the assistant
// block hugs the surrounding transcript spacing.
func splitGlamourLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(xansi.Strip(lines[start])) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(xansi.Strip(lines[end-1])) == "" {
		end--
	}
	if start >= end {
		return []string{""}
	}
	return lines[start:end]
}

func glamourCacheKey(measure int, text string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return strconv.Itoa(glamourThemeGen) + ":" + strconv.Itoa(measure) + ":" + strconv.FormatUint(h.Sum64(), 36)
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
