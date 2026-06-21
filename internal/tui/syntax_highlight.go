package tui

import (
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
	if measure < 4 {
		return nil, false
	}
	lexer := cachedLexer(lang)
	if lexer == nil {
		return nil, false
	}
	iterator, err := lexer.Tokenise(nil, strings.Join(code, "\n"))
	if err != nil {
		return nil, false
	}

	lines := []string{}
	var cur strings.Builder
	curWidth := 0
	flushLine := func() {
		lines = append(lines, cur.String())
		cur.Reset()
		curWidth = 0
	}
	emit := func(style lipgloss.Style, s string) {
		var chunk strings.Builder
		flushChunk := func() {
			if chunk.Len() > 0 {
				cur.WriteString(style.Render(chunk.String()))
				chunk.Reset()
			}
		}
		for _, r := range s {
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

	for _, token := range iterator.Tokens() {
		style := tokenStyle(token.Type)
		for index, part := range strings.Split(token.Value, "\n") {
			if index > 0 {
				flushLine()
			}
			emit(style, part)
		}
	}
	flushLine()
	// chroma emits a trailing newline -> a final empty line; drop it.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines, true
}
