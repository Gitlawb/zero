package lsp

import (
	"runtime"
	"strings"
	"testing"
)

func TestURIRoundTrip(t *testing.T) {
	// Use an OS-appropriate absolute path so the round-trip is platform-correct,
	// including a space to exercise percent-encoding.
	path := "/home/dev/a b.go"
	if runtime.GOOS == "windows" {
		path = `C:\Users\dev\a b.go`
	}

	uri := PathToURI(path)
	if !strings.HasPrefix(uri, "file://") {
		t.Fatalf("PathToURI(%q) = %q, want file:// scheme", path, uri)
	}
	if strings.Contains(uri, " ") {
		t.Fatalf("URI should percent-encode spaces, got %q", uri)
	}
	if got := URIToPath(uri); got != path {
		t.Fatalf("round-trip failed: %q -> %q -> %q", path, uri, got)
	}
}

func TestURIToPathPassesThroughNonFileURI(t *testing.T) {
	if got := URIToPath("https://example.com/x"); got != "https://example.com/x" {
		t.Fatalf("non-file URI should pass through, got %q", got)
	}
}

func TestPathToURIEmpty(t *testing.T) {
	if PathToURI("") != "" {
		t.Fatal("empty path should yield empty URI")
	}
}
