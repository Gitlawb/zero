package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const sampleHTML = `<!DOCTYPE html>
<html><head><title>ignored</title><style>body{color:red}</style></head>
<body>
<script>var tracking = "noise";</script>
<h1>Main Title</h1>
<p>First paragraph with a <a href="https://example.com/docs">docs link</a> and &amp; entity.</p>
<ul><li>alpha</li><li>beta</li></ul>
<!-- hidden comment -->
<h2>Section</h2>
<p>Body   with   extra   spaces.</p>
</body></html>`

func TestHTMLToMarkdown(t *testing.T) {
	markdown := htmlToMarkdown(sampleHTML)

	for _, want := range []string{
		"# Main Title",
		"## Section",
		"[docs link](https://example.com/docs)",
		"- alpha",
		"- beta",
		"with a", // paragraph text survives
		"& entity",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
	for _, banned := range []string{"tracking", "color:red", "hidden comment", "<p>", "</html>", "ignored"} {
		if strings.Contains(markdown, banned) {
			t.Fatalf("markdown must not contain %q:\n%s", banned, markdown)
		}
	}
	if strings.Contains(markdown, "extra   spaces") {
		t.Fatalf("space runs must collapse:\n%s", markdown)
	}
	if strings.Contains(markdown, "\n\n\n") {
		t.Fatalf("blank-line runs must be capped:\n%s", markdown)
	}
}

func TestLooksLikeHTML(t *testing.T) {
	cases := []struct {
		contentType string
		body        string
		want        bool
	}{
		{"text/html; charset=utf-8", "anything", true},
		{"application/xhtml+xml", "anything", true},
		{"application/json", `{"html":"<html>"}`, false},
		{"text/plain", "<!doctype html><html>", true}, // mislabeled HTML sniffed
		{"text/plain", "just text", false},
		{"", "<html><body>x</body></html>", true},
	}
	for _, c := range cases {
		if got := looksLikeHTML(c.contentType, c.body); got != c.want {
			t.Errorf("looksLikeHTML(%q, %.20q) = %v, want %v", c.contentType, c.body, got, c.want)
		}
	}
}

func TestWebFetchConvertsHTMLByDefault(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		return webFetchTestResponse(request, http.StatusOK, "text/html; charset=utf-8", sampleHTML), nil
	}))

	result := tool.Run(context.Background(), map[string]any{"url": "https://example.com/page"})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "# Main Title") || strings.Contains(result.Output, "<h1>") {
		t.Fatalf("HTML must convert to markdown by default:\n%s", result.Output)
	}
	if result.Meta["converted"] != "true" {
		t.Fatalf("converted meta must be true: %#v", result.Meta)
	}
}

func TestWebFetchRawFormatSkipsConversion(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		return webFetchTestResponse(request, http.StatusOK, "text/html", sampleHTML), nil
	}))

	result := tool.Run(context.Background(), map[string]any{"url": "https://example.com/page", "format": "raw"})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "<h1>Main Title</h1>") {
		t.Fatalf("format=raw must keep original HTML:\n%s", result.Output)
	}
	if result.Meta["converted"] != "false" {
		t.Fatalf("converted meta must be false: %#v", result.Meta)
	}
}

func TestWebFetchLeavesNonHTMLUntouched(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		return webFetchTestResponse(request, http.StatusOK, "application/json", `{"key":"<b>value</b>"}`), nil
	}))

	result := tool.Run(context.Background(), map[string]any{"url": "https://example.com/api"})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, `{"key":"<b>value</b>"}`) {
		t.Fatalf("non-HTML must pass through untouched:\n%s", result.Output)
	}
}

func TestWebFetchRejectsInvalidFormat(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		return webFetchTestResponse(request, http.StatusOK, "text/plain", "x"), nil
	}))
	result := tool.Run(context.Background(), map[string]any{"url": "https://example.com", "format": "xml"})
	if result.Status != StatusError || !strings.Contains(result.Output, "format must be") {
		t.Fatalf("invalid format must be rejected: %s %s", result.Status, result.Output)
	}
}
