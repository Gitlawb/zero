package tui

import (
	"image/color"
	"path/filepath"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

var (
	lexerCacheMu sync.RWMutex
	lexerCache   = map[string]chroma.Lexer{}
)

// cachedLexer resolves a language name to a chroma lexer once, memoizing the
// result — including a nil "no lexer" result — so chroma's per-language registry
// Match scan (which its own docs call slow) runs at most once per language. An
// empty language short-circuits before any lookup, which is the common case for
// a bare ``` fence on every streaming frame.
func cachedLexer(lang string) chroma.Lexer {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return nil
	}
	lexerCacheMu.RLock()
	lexer, ok := lexerCache[lang]
	lexerCacheMu.RUnlock()
	if ok {
		return lexer
	}
	lexer = lexers.Get(lang)
	lexerCacheMu.Lock()
	lexerCache[lang] = lexer
	lexerCacheMu.Unlock()
	return lexer
}

func cachedLexerForPath(path string) chroma.Lexer {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	key := "path:" + strings.ToLower(filepath.Base(path))
	lexerCacheMu.RLock()
	lexer, ok := lexerCache[key]
	lexerCacheMu.RUnlock()
	if ok {
		return lexer
	}
	lexer = lexers.Match(path)
	lexerCacheMu.Lock()
	lexerCache[key] = lexer
	lexerCacheMu.Unlock()
	return lexer
}

// tokenStyle maps a chroma token type onto Zero's existing, contrast-audited
// palette rather than a chroma color scheme — so highlighted code stays on-brand
// and degrades through the same lipgloss profile path as the rest of the UI
// (truecolor → 256 → 16 → plain on no-TTY).
func tokenStyle(tt chroma.TokenType) lipgloss.Style {
	switch {
	case tt.InCategory(chroma.Keyword):
		return zeroTheme.accent
	case tt.InCategory(chroma.Comment):
		return zeroTheme.faint
	case tt.InSubCategory(chroma.LiteralString):
		return zeroTheme.green
	case tt.InSubCategory(chroma.LiteralNumber):
		return zeroTheme.amber
	case tt == chroma.NameFunction || tt == chroma.NameClass || tt == chroma.NameBuiltin || tt == chroma.NameNamespace || tt == chroma.NameDecorator:
		return zeroTheme.blue
	case tt.InCategory(chroma.Operator), tt.InCategory(chroma.Punctuation):
		return zeroTheme.muted
	default:
		return zeroTheme.ink
	}
}

// highlightCode syntax-highlights a fenced code block, wrapping at measure cells
// while carrying each token's color across wrap boundaries. ok is false when no
// lexer matches the language (the caller then renders the block plain), so an
// unknown or missing language is never worse than today. Wrapping is done at the
// token level so colors never split an ANSI escape.
func highlightCode(code []string, lang string, measure int) ([]string, bool) {
	return highlightCodeWithLexer(cachedLexer(lang), code, measure, nil)
}

func highlightCodeAuto(code []string, lang string, measure int) ([]string, bool) {
	if strings.TrimSpace(lang) == "" {
		lang = inferCodeLanguage(code)
	}
	return highlightCode(code, lang, measure)
}

func highlightCodeForPath(code []string, path string, measure int, bg color.Color) ([]string, bool) {
	return highlightCodeWithLexer(cachedLexerForPath(path), code, measure, bg)
}

// highlightSpan overlays a background on a rune range in one source line while
// retaining the lexer-selected foreground. Diff rendering uses it to keep the
// precise changed word visible inside a syntax-highlighted add/delete row.
type highlightSpan struct {
	line       int
	start, end int
	background color.Color
}

func highlightCodeForPathWithSpans(code []string, path string, measure int, bg color.Color, spans []highlightSpan) ([]string, bool) {
	return highlightCodeWithLexerAndSpans(cachedLexerForPath(path), code, measure, bg, spans)
}

// highlightCodeForPathWithLineBackgrounds applies syntax highlighting to a
// single source unit while preserving a distinct background for every source
// line. The diff viewer uses one lexer pass per hunk so multiline syntax keeps
// its state across changed and unchanged lines.
func highlightCodeForPathWithLineBackgrounds(code []string, path string, measure int, backgrounds []color.Color, spans []highlightSpan) ([]string, bool) {
	return highlightCodeWithLexerAndLineBackgrounds(cachedLexerForPath(path), code, measure, backgrounds, spans)
}

func inferCodeLanguage(code []string) string {
	for _, line := range code {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "from ") && strings.Contains(trimmed, " import "):
			return "python"
		case strings.HasPrefix(trimmed, "import ") && !strings.Contains(trimmed, " from "):
			return "python"
		case strings.HasPrefix(trimmed, "def ") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case strings.HasPrefix(trimmed, "class ") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case strings.HasPrefix(trimmed, "if ") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case strings.HasPrefix(trimmed, "elif ") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case trimmed == "else:" || trimmed == "try:" || trimmed == "finally:":
			return "python"
		case strings.HasPrefix(trimmed, "for ") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case strings.HasPrefix(trimmed, "while ") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case strings.HasPrefix(trimmed, "with ") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case strings.HasPrefix(trimmed, "except") && strings.HasSuffix(trimmed, ":"):
			return "python"
		case strings.HasPrefix(trimmed, "return "):
			return "python"
		case strings.HasPrefix(trimmed, "print("):
			return "python"
		case strings.HasPrefix(trimmed, "package "):
			return "go"
		case strings.HasPrefix(trimmed, "func ") && strings.Contains(trimmed, "{"):
			return "go"
		case strings.HasPrefix(trimmed, "const "), strings.HasPrefix(trimmed, "let "), strings.HasPrefix(trimmed, "var "):
			return "javascript"
		case strings.HasPrefix(trimmed, "function ") && strings.Contains(trimmed, "{"):
			return "javascript"
		case strings.HasPrefix(trimmed, "<!DOCTYPE "), strings.HasPrefix(trimmed, "<html"), strings.HasPrefix(trimmed, "<div"), strings.HasPrefix(trimmed, "<span"):
			return "html"
		}
		break
	}
	return ""
}

func highlightCodeWithLexer(lexer chroma.Lexer, code []string, measure int, bg color.Color) ([]string, bool) {
	return highlightCodeWithLexerAndSpans(lexer, code, measure, bg, nil)
}

func highlightCodeWithLexerAndSpans(lexer chroma.Lexer, code []string, measure int, bg color.Color, spans []highlightSpan) ([]string, bool) {
	backgrounds := make([]color.Color, len(code))
	for index := range backgrounds {
		backgrounds[index] = bg
	}
	return highlightCodeWithLexerAndLineBackgrounds(lexer, code, measure, backgrounds, spans)
}

func highlightCodeWithLexerAndLineBackgrounds(lexer chroma.Lexer, code []string, measure int, backgrounds []color.Color, spans []highlightSpan) ([]string, bool) {
	if measure < 4 {
		return nil, false
	}
	if lexer == nil {
		return nil, false
	}
	iterator, err := lexer.Tokenise(nil, strings.Join(code, "\n"))
	if err != nil {
		return nil, false
	}

	lines := []string{}
	spansByLine := make(map[int][]highlightSpan, len(spans))
	for _, span := range spans {
		spansByLine[span.line] = append(spansByLine[span.line], span)
	}
	var cur strings.Builder
	curWidth := 0
	flushLine := func() {
		lines = append(lines, cur.String())
		cur.Reset()
		curWidth = 0
	}
	emit := func(style lipgloss.Style, s string, line, column int) {
		var chunk strings.Builder
		var chunkStyle lipgloss.Style
		var chunkBackground color.Color
		var haveChunkStyle bool
		flushChunk := func() {
			if chunk.Len() > 0 {
				cur.WriteString(chunkStyle.Render(chunk.String()))
				chunk.Reset()
				haveChunkStyle = false
				chunkBackground = nil
			}
		}
		for offset, r := range []rune(s) {
			var background color.Color
			if line < len(backgrounds) {
				background = backgrounds[line]
			}
			for _, span := range spansByLine[line] {
				if column+offset >= span.start && column+offset < span.end {
					background = span.background
					break
				}
			}
			runeStyle := style
			if background != nil {
				runeStyle = runeStyle.Background(background)
			}
			if !haveChunkStyle {
				chunkStyle = runeStyle
				chunkBackground = background
				haveChunkStyle = true
			} else if !sameHighlightColor(background, chunkBackground) {
				flushChunk()
				chunkStyle = runeStyle
				chunkBackground = background
				haveChunkStyle = true
			}
			rw := lipgloss.Width(string(r))
			if curWidth+rw > measure {
				flushChunk()
				flushLine()
			}
			chunk.WriteString(string(r))
			curWidth += rw
		}
		flushChunk()
	}

	line, column := 0, 0
	for _, token := range iterator.Tokens() {
		style := tokenStyle(token.Type)
		for index, part := range strings.Split(token.Value, "\n") {
			if index > 0 {
				flushLine()
				line++
				column = 0
			}
			emit(style, part, line, column)
			column += len([]rune(part))
		}
	}
	flushLine()
	// chroma emits a trailing newline -> a final empty line; drop it.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines, true
}

func sameHighlightColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
