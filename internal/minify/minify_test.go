package minify

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestMinifyGoStripsCommentsKeepsValidCode(t *testing.T) {
	src := `package demo

import "fmt"

// Greet returns a greeting. This doc comment MUST be removed.
func Greet(name string) string {
	// inline comment to drop
	msg := fmt.Sprintf("hi %s", name) // trailing comment
	return msg
}
`
	r := File("x.go", []byte(src))
	if !r.Applied || r.Language != "go" {
		t.Fatalf("expected go minification, got %+v", r)
	}
	for _, c := range []string{"MUST be removed", "inline comment", "trailing comment", "//"} {
		if strings.Contains(r.Content, c) {
			t.Errorf("comment text leaked: %q\n%s", c, r.Content)
		}
	}
	for _, code := range []string{"func Greet", "fmt.Sprintf", "return msg"} {
		if !strings.Contains(r.Content, code) {
			t.Errorf("code dropped: %q\n%s", code, r.Content)
		}
	}
	// The minified output must still be valid Go.
	if _, err := parser.ParseFile(token.NewFileSet(), "", r.Content, 0); err != nil {
		t.Fatalf("minified Go does not parse: %v\n%s", err, r.Content)
	}
}

func TestMinifyGoStatementFragment(t *testing.T) {
	r := File("snippet.go", []byte("x := 1   \n\n\n// keep me\ny := 2\n"))
	if !r.Applied || r.Language != "go-fragment" {
		t.Fatalf("expected parsed Go fragment, got %+v", r)
	}
	if strings.Contains(r.Content, "\n\n\n") {
		t.Errorf("fragment should collapse blank runs:\n%q", r.Content)
	}
	if strings.Contains(r.Content, "keep me") || !strings.Contains(r.Content, "y := 2") {
		t.Errorf("fragment was not compacted safely: %q", r.Content)
	}
}

func TestMinifyGoFragmentAccepts512LinesWithFinalNewline(t *testing.T) {
	src := strings.Repeat("x++\n", 512)
	r := File("snippet.go", []byte(src))
	if !r.Applied || r.Language != "go-fragment" {
		t.Fatalf("expected 512-line fragment to remain eligible, got %+v", r)
	}
	if got := strings.Count(r.Content, "x++"); got != 512 {
		t.Fatalf("compacted statement count = %d, want 512", got)
	}
}

func TestMinifyGoDeclarationPrefixPreservesIncompleteTail(t *testing.T) {
	src := `func Handle737(n int) (int, error) {
	if n%2 == 1 {
		return 0, errBad
	}
	return n + 737, nil
}

// Handle738 begins outside the requested range.
func Handle738(n int) (int, error) {
	if n%2 == 1 {`
	r := File("range.go", []byte(src))
	if !r.Applied || r.Language != "go-fragment" {
		t.Fatalf("expected declaration-prefix compaction, got %+v", r)
	}
	if strings.Contains(r.Content, "Handle737 returns") || !strings.Contains(r.Content, "return n + 737") {
		t.Fatalf("complete declaration was not compacted correctly:\n%s", r.Content)
	}
	if !strings.Contains(r.Content, "func Handle738") || !strings.Contains(r.Content, "if n%2 == 1 {") {
		t.Fatalf("incomplete tail was not preserved:\n%s", r.Content)
	}
}

func TestMinifyGoLargeInvalidInputFallsBackConservatively(t *testing.T) {
	src := strings.Repeat("x := 1 // keep\n", 513)
	r := File("broken.go", []byte(src))
	if r.Applied || !strings.Contains(r.Content, "// keep") {
		t.Fatalf("large invalid input must retain conservative fallback: %+v", r)
	}
}

func TestMinifyGenericCollapsesBlanksAndTrims(t *testing.T) {
	r := File("notes.txt", []byte("a   \n\n\n\nb\t\n"))
	if r.Applied {
		t.Fatalf("text is not 'applied' minification")
	}
	if r.Content != "a\n\nb" {
		t.Fatalf("generic = %q, want %q", r.Content, "a\n\nb")
	}
}
