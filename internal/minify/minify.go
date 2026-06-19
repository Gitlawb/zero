// Package minify produces a denser, token-cheaper VIEW of a source file for
// read-only exploration. It strips comments and redundant whitespace so the
// model can scan or understand code for far fewer tokens than read_file's raw,
// line-numbered output — without ever changing the file on disk.
//
// Correctness over aggression: Go is minified through the real go/parser AST
// (guaranteed valid, comment-free output), and any file it cannot parse — or any
// non-Go file — falls back to a conservative whitespace normalization that can
// never alter code meaning (it strips no comments, since doing that safely needs
// a real parser per language). The transform is therefore incapable of corrupting
// what the model sees: the worst case is "no reduction", never "wrong content".
package minify

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
)

// Result is the outcome of minifying one file's bytes.
type Result struct {
	Content  string // minified (or whitespace-normalized) text; never line-numbered
	Language string // strategy taken: "go" or "text"
	Applied  bool   // true only when real comment-stripping minification ran
}

// File minifies content addressed by path; the extension selects the strategy.
func File(path string, content []byte) Result {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".go" {
		if out, ok := minifyGo(content); ok {
			return Result{Content: out, Language: "go", Applied: true}
		}
		// Unparsable Go (a snippet, generics edge, or syntax error): fall through
		// to the safe generic path rather than risk a partial AST reprint.
	} else if style, ok := commentStyles[ext]; ok {
		// Strip comments with a string-aware lexer, then collapse the whitespace the
		// removed comments left behind. The stripper only handles languages whose
		// string forms it models exactly, so it cannot corrupt content.
		stripped := stripComments(string(content), style)
		return Result{Content: minifyGeneric([]byte(stripped)), Language: style.name, Applied: true}
	}
	return Result{Content: minifyGeneric(content), Language: "text", Applied: false}
}

// minifyGo parses Go WITHOUT comments (omitting parser.ParseComments leaves them
// unattached) and reprints the AST, yielding valid, comment-free, gofmt-shaped
// source with tab indentation (1 char per level). It returns ok=false on any
// parse error so the caller falls back to the raw text — a non-package snippet or
// syntactically invalid file is never mangled.
func minifyGo(content []byte) (string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.SkipObjectResolution)
	if err != nil {
		return "", false
	}
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.TabIndent, Tabwidth: 1}
	if err := cfg.Fprint(&buf, fset, file); err != nil {
		return "", false
	}
	return strings.TrimRight(buf.String(), "\n"), true
}

// minifyGeneric is the safe fallback for non-Go (and unparsable Go) content: it
// normalizes CRLF, trims trailing whitespace, and collapses runs of blank lines
// to a single blank — removing easy bloat with zero risk to code semantics. It
// deliberately strips NO comments.
func minifyGeneric(content []byte) string {
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	out := make([]string, 0, len(lines))
	blankRun := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blankRun++
			if blankRun > 1 {
				continue
			}
		} else {
			blankRun = 0
		}
		out = append(out, trimmed)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
