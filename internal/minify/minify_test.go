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

func TestMinifyGoFallsBackOnUnparsableGo(t *testing.T) {
	// A snippet (no package clause) cannot parse as a file -> safe generic path.
	r := File("snippet.go", []byte("x := 1   \n\n\n// keep me\ny := 2\n"))
	if r.Applied {
		t.Fatalf("expected fallback for unparsable Go, got %+v", r)
	}
	if strings.Contains(r.Content, "\n\n\n") {
		t.Errorf("generic should collapse blank runs:\n%q", r.Content)
	}
	if !strings.Contains(r.Content, "// keep me") {
		t.Errorf("generic must NOT strip comments (unsafe without a parser): %q", r.Content)
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
