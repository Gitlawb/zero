package minify

import "strings"

// commentStyle describes one language's comment + string lexical rules for the
// string-aware stripper. ONLY languages whose every string form this scanner can
// model exactly appear in commentStyles. Languages with raw-string delimiters
// (C++ R"(...)", C# @"..."), lifetimes (Rust 'a), template literals (JS/TS
// `${...}`), nested block comments (Rust/Swift), or heredocs (shell/Ruby) are
// deliberately omitted — they fall back to whitespace-only minification — so the
// stripper can never mistake code or string content for a comment, nor a comment
// for code. Correctness over coverage.
type commentStyle struct {
	name       string
	line       []string // line-comment markers; the rest of the line is dropped
	blockOpen  string   // "" when the language has no block comments
	blockClose string
	triples    []string // raw multi-line string delimiters (Python ''' """, Java text block """)
	fStrings   bool     // Python f-strings: a quote inside {…} does not end the string
}

// commentStyles maps a file extension to its stripper config. Every entry here is
// covered by golden tests in comments_test.go, including the tricky cases
// (comment chars inside strings, strings inside comments, escapes, triple-quotes,
// f-string same-quote nesting, Java text blocks).
var commentStyles = map[string]commentStyle{
	".c":    {name: "c", line: []string{"//"}, blockOpen: "/*", blockClose: "*/"},
	".java": {name: "java", line: []string{"//"}, blockOpen: "/*", blockClose: "*/", triples: []string{`"""`}},
	".css":  {name: "css", blockOpen: "/*", blockClose: "*/"},
	".scss": {name: "scss", line: []string{"//"}, blockOpen: "/*", blockClose: "*/"},
	".less": {name: "less", line: []string{"//"}, blockOpen: "/*", blockClose: "*/"},
	".py":   {name: "python", line: []string{"#"}, triples: []string{`"""`, `'''`}, fStrings: true},
	".pyi":  {name: "python", line: []string{"#"}, triples: []string{`"""`, `'''`}, fStrings: true},
}

// stripComments removes line and block comments from src for one language while
// correctly skipping string, char, triple-quoted, and (Python) f-string literals,
// so a comment marker inside a literal is never stripped and literal content is
// never mistaken for a comment. Delimiters are all ASCII, so scanning byte-wise is
// safe — UTF-8 content is copied verbatim.
func stripComments(src string, style commentStyle) string {
	var out strings.Builder
	out.Grow(len(src))
	i, n := 0, len(src)
	for i < n {
		// Raw multi-line strings first: """ must win over a bare " literal.
		if d, ok := longestPrefix(src[i:], style.triples); ok {
			j := scanDelimited(src, i+len(d), d)
			out.WriteString(src[i:j])
			i = j
			continue
		}
		// Block comment: drop through the close marker (or to EOF if unterminated).
		if style.blockOpen != "" && strings.HasPrefix(src[i:], style.blockOpen) {
			rest := src[i+len(style.blockOpen):]
			if end := strings.Index(rest, style.blockClose); end >= 0 {
				i += len(style.blockOpen) + end + len(style.blockClose)
			} else {
				i = n
			}
			continue
		}
		// Line comment: drop to the newline, which is emitted on the next iteration.
		if _, ok := longestPrefix(src[i:], style.line); ok {
			if nl := strings.IndexByte(src[i:], '\n'); nl >= 0 {
				i += nl
			} else {
				i = n
			}
			continue
		}
		// String / char literal.
		if c := src[i]; c == '"' || c == '\'' {
			j := scanString(src, i, style.fStrings)
			out.WriteString(src[i:j])
			i = j
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String()
}

// longestPrefix reports the longest candidate that prefixes s (markers are short
// and few, so a linear scan is fine).
func longestPrefix(s string, candidates []string) (string, bool) {
	best := ""
	for _, c := range candidates {
		if len(c) > len(best) && strings.HasPrefix(s, c) {
			best = c
		}
	}
	return best, best != ""
}

// scanDelimited returns the index just past the closing delim, starting from
// bodyStart (just past the opening delim). A backslash escapes the next byte so a
// \-escaped delimiter does not terminate; an unterminated literal runs to EOF.
func scanDelimited(src string, bodyStart int, delim string) int {
	j, n := bodyStart, len(src)
	for j < n {
		if src[j] == '\\' {
			j += 2
			continue
		}
		if strings.HasPrefix(src[j:], delim) {
			return j + len(delim)
		}
		j++
	}
	return n
}

// scanString returns the index just past a closing quote for the string/char
// literal that opens at i. Backslash escapes the next byte. When fStrings is set
// and the literal is an f-string (an f/F prefix precedes the quote), a quote that
// sits inside a {…} replacement field does NOT close the literal — and a nested
// string inside that field is scanned in turn — so Python 3.12 same-quote-nested
// f-strings are handled correctly.
func scanString(src string, i int, fStrings bool) int {
	quote := src[i]
	isF := fStrings && hasFStringPrefix(src, i)
	j, n := i+1, len(src)
	brace := 0
	for j < n {
		c := src[j]
		if c == '\\' {
			j += 2
			continue
		}
		if isF {
			switch c {
			case '{':
				if j+1 < n && src[j+1] == '{' { // {{ is a literal brace
					j += 2
					continue
				}
				brace++
				j++
				continue
			case '}':
				if j+1 < n && src[j+1] == '}' {
					j += 2
					continue
				}
				if brace > 0 {
					brace--
				}
				j++
				continue
			case '"', '\'':
				if brace > 0 { // a nested string inside the replacement field
					j = scanString(src, j, false)
					continue
				}
			}
		}
		if c == quote && brace == 0 {
			return j + 1
		}
		j++
	}
	return n
}

// hasFStringPrefix reports whether the quote at i is preceded by a Python string
// prefix containing f/F (e.g. f", rf", Rf"), bounded so a quote that merely
// follows an identifier ending in f is not misread as an f-string.
func hasFStringPrefix(src string, i int) bool {
	k := i - 1
	sawF := false
	for k >= 0 {
		switch src[k] {
		case 'f', 'F':
			sawF = true
		case 'r', 'R', 'b', 'B', 'u', 'U':
		default:
			goto bounded
		}
		k--
	}
bounded:
	return sawF && (k < 0 || !isIdentByte(src[k]))
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
