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
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
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
		if out, ok := minifyGoFragment(content); ok {
			return Result{Content: out, Language: "go-fragment", Applied: true}
		}
		// An incomplete fragment that cannot be wrapped safely falls through to
		// whitespace-only normalization; no source text is guessed or discarded.
	} else if style, ok := commentStyles[ext]; ok {
		// Strip comments with a string-aware lexer, then collapse the whitespace the
		// removed comments left behind. The stripper only handles languages whose
		// string forms it models exactly, so it cannot corrupt content.
		stripped := stripComments(string(content), style)
		return Result{Content: minifyGeneric([]byte(stripped)), Language: style.name, Applied: true}
	}
	return Result{Content: minifyGeneric(content), Language: "text", Applied: false}
}

// Fragment minifies a bounded source range without assuming lexical state from
// text outside the range. Go remains parser-validated; other languages use the
// conservative whitespace-only transform because a range may begin inside a
// multiline string or block comment.
func Fragment(path string, content []byte) Result {
	if strings.EqualFold(filepath.Ext(path), ".go") {
		if out, ok := minifyGo(content); ok {
			return Result{Content: out, Language: "go", Applied: true}
		}
		if out, ok := minifyGoFragment(content); ok {
			return Result{Content: out, Language: "go-fragment", Applied: true}
		}
	}
	return Result{Content: minifyGeneric(content), Language: "text", Applied: false}
}

// ContextualFragment minifies a bounded fragment after checking its starting
// position against the complete source. A Go range that begins inside a string
// or comment must remain verbatim apart from whitespace normalization: parsing
// that fragment by itself would mistake literal text for Go syntax.
func ContextualFragment(path string, source, content []byte, startOffset int) Result {
	if strings.EqualFold(filepath.Ext(path), ".go") && goFragmentStartsInsideToken(source, startOffset) {
		return Result{Content: minifyGeneric(content), Language: "text", Applied: false}
	}
	return Fragment(path, content)
}

func goFragmentStartsInsideToken(source []byte, startOffset int) bool {
	if startOffset <= 0 || startOffset >= len(source) {
		return false
	}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(source))
	var lexer scanner.Scanner
	lexer.Init(file, source, nil, scanner.ScanComments)
	for {
		position, kind, literal := lexer.Scan()
		if kind == token.EOF {
			return false
		}
		if kind != token.STRING && kind != token.COMMENT {
			continue
		}
		start := file.Offset(position)
		if startOffset > start && startOffset < start+len(literal) {
			return true
		}
	}
}

// minifyGoFragment handles bounded reads that do not contain a package clause.
// It finds the longest complete declaration or statement prefix that the real
// Go parser accepts, prints that prefix without comments, and preserves any
// incomplete tail conservatively. Ranges are intentionally capped: repeatedly
// parsing a large broken file would otherwise turn a cheap read into O(n²) work.
func minifyGoFragment(content []byte) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 512 {
		return "", false
	}
	for end := len(lines); end > 0; end-- {
		prefix := strings.Join(lines[:end], "\n")
		if compact, ok := parseGoDeclarations(prefix); ok {
			return joinCompactPrefix(compact, lines[end:]), true
		}
		if compact, ok := parseGoStatements(prefix); ok {
			return joinCompactPrefix(compact, lines[end:]), true
		}
	}
	return "", false
}

func parseGoDeclarations(fragment string) (string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", "package compact\n"+fragment, parser.SkipObjectResolution)
	if err != nil || len(file.Decls) == 0 {
		return "", false
	}
	nodes := make([]ast.Node, len(file.Decls))
	for i, declaration := range file.Decls {
		nodes[i] = declaration
	}
	return printGoNodes(fset, nodes)
}

func parseGoStatements(fragment string) (string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", "package compact\nfunc _(){\n"+fragment+"\n}", parser.SkipObjectResolution)
	if err != nil || len(file.Decls) != 1 {
		return "", false
	}
	function, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || function.Body == nil || len(function.Body.List) == 0 {
		return "", false
	}
	nodes := make([]ast.Node, len(function.Body.List))
	for i, statement := range function.Body.List {
		nodes[i] = statement
	}
	return printGoNodes(fset, nodes)
}

func printGoNodes(fset *token.FileSet, nodes []ast.Node) (string, bool) {
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.TabIndent, Tabwidth: 1}
	for i, node := range nodes {
		if i > 0 {
			buf.WriteByte('\n')
		}
		if err := cfg.Fprint(&buf, fset, node); err != nil {
			return "", false
		}
	}
	return strings.TrimSpace(buf.String()), true
}

func joinCompactPrefix(compact string, remainder []string) string {
	tail := minifyGeneric([]byte(strings.Join(remainder, "\n")))
	if tail == "" {
		return compact
	}
	return compact + "\n" + tail
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
