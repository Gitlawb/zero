package tools

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type webFetchResolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (fn webFetchResolverFunc) LookupNetIP(ctx context.Context, network string, host string) ([]netip.Addr, error) {
	return fn(ctx, network, host)
}

func TestWebFetchToolSafetyAndSchema(t *testing.T) {
	tool := NewWebFetchTool()

	if tool.Name() != "web_fetch" {
		t.Fatalf("Name = %q, want web_fetch", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("Description is empty")
	}
	safety := tool.Safety()
	if safety.SideEffect != SideEffectNetwork || safety.Permission != PermissionPrompt || !safety.AdvertiseInAuto {
		t.Fatalf("unexpected safety metadata: %#v", safety)
	}
	if safety.Reason == "" {
		t.Fatal("Safety reason is empty")
	}

	schema := tool.Parameters()
	if schema.Type != "object" || schema.AdditionalProperties {
		t.Fatalf("unexpected schema envelope: %#v", schema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "url" {
		t.Fatalf("required fields = %#v, want url only", schema.Required)
	}
	if schema.Properties["url"].Type != "string" {
		t.Fatalf("url schema = %#v, want string", schema.Properties["url"])
	}
	maxBytes := schema.Properties["max_bytes"]
	if maxBytes.Type != "integer" || maxBytes.Minimum == nil || *maxBytes.Minimum != 1 || maxBytes.Maximum == nil {
		t.Fatalf("max_bytes schema = %#v, want bounded integer", maxBytes)
	}
}

func TestWebFetchToolFetchesHTTPText(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", request.Method)
		}
		if request.Header.Get("User-Agent") == "" {
			t.Fatal("expected User-Agent header")
		}
		return webFetchTestResponse(request, http.StatusOK, "text/plain; charset=utf-8", "hello zero"), nil
	}))

	result := tool.Run(context.Background(), map[string]any{
		"url": "https://example.com/guide?token=secret-token",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	for _, want := range []string{"URL: https://example.com/guide?token=[REDACTED]", "Status: 200 OK", "Content-Type: text/plain; charset=utf-8", "hello zero"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, result.Output)
		}
	}
	if strings.Contains(result.Output, "secret-token") {
		t.Fatalf("expected URL secrets to be redacted, got %q", result.Output)
	}
	if result.Meta["status_code"] != "200" || result.Meta["content_type"] != "text/plain; charset=utf-8" || result.Meta["truncated"] != "false" {
		t.Fatalf("unexpected metadata: %#v", result.Meta)
	}
}

func TestWebFetchToolTruncatesAtMaxBytes(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		return webFetchTestResponse(request, http.StatusOK, "text/plain", "abcdefg"), nil
	}))

	result := tool.Run(context.Background(), map[string]any{
		"url":       "https://example.com/long",
		"max_bytes": 4,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	if !result.Truncated || result.Meta["truncated"] != "true" || result.Meta["bytes"] != "4" {
		t.Fatalf("expected truncation metadata, got truncated=%v meta=%#v", result.Truncated, result.Meta)
	}
	if !strings.Contains(result.Output, "abcd") || strings.Contains(result.Output, "efg") {
		t.Fatalf("expected output to contain only truncated body, got %q", result.Output)
	}
}

func TestWebFetchToolRejectsUnsafeURLsBeforeNetwork(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsafe URL should be rejected before network transport")
		return nil, nil
	}))

	for _, rawURL := range []string{
		"file:///tmp/secret",
		"ftp://example.com/file",
		"http://127.0.0.1/admin",
		"http://localhost/status",
		"http://169.254.169.254/latest/meta-data",
		"http://user:pass@example.com/private",
	} {
		t.Run(rawURL, func(t *testing.T) {
			result := tool.Run(context.Background(), map[string]any{"url": rawURL})
			if result.Status != StatusError {
				t.Fatalf("expected unsafe URL error, got %s: %s", result.Status, result.Output)
			}
			if !strings.Contains(result.Output, "Unsafe URL") {
				t.Fatalf("expected unsafe URL message, got %q", result.Output)
			}
		})
	}
}

func TestWebFetchToolRejectsHostnamesResolvingToPrivateAddresses(t *testing.T) {
	tool := newWebFetchToolWithClientAndResolver(
		webFetchTestClient(func(*http.Request) (*http.Response, error) {
			t.Fatal("private resolved host should be rejected before network transport")
			return nil, nil
		}),
		webFetchResolverFunc(func(_ context.Context, network string, host string) ([]netip.Addr, error) {
			if network != "ip" || host != "private.example" {
				t.Fatalf("unexpected lookup network=%q host=%q", network, host)
			}
			return []netip.Addr{netip.MustParseAddr("10.0.0.12")}, nil
		}),
	)

	result := tool.Run(context.Background(), map[string]any{"url": "https://private.example/status"})

	if result.Status != StatusError {
		t.Fatalf("expected private resolved host error, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "private network hosts are blocked") {
		t.Fatalf("expected private host message, got %q", result.Output)
	}
}

func TestWebFetchToolRejectsUnsafeRedirects(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		response := webFetchTestResponse(request, http.StatusFound, "text/plain", "redirect")
		response.Header.Set("Location", "http://127.0.0.1/private")
		return response, nil
	}))

	result := tool.Run(context.Background(), map[string]any{"url": "https://example.com/start"})

	if result.Status != StatusError {
		t.Fatalf("expected redirect safety error, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "Unsafe redirect URL") {
		t.Fatalf("expected unsafe redirect message, got %q", result.Output)
	}
}

func TestWebFetchToolRejectsNonSuccessStatus(t *testing.T) {
	tool := newWebFetchToolWithClient(webFetchTestClient(func(request *http.Request) (*http.Response, error) {
		return webFetchTestResponse(request, http.StatusNotFound, "text/plain", "missing page"), nil
	}))

	result := tool.Run(context.Background(), map[string]any{"url": "https://example.com/missing"})

	if result.Status != StatusError {
		t.Fatalf("expected status error, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "HTTP 404 Not Found") || !strings.Contains(result.Output, "missing page") {
		t.Fatalf("unexpected non-success output: %q", result.Output)
	}
}

func webFetchTestClient(handler func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTripFunc(handler)}
}

func webFetchTestResponse(request *http.Request, statusCode int, contentType string, body string) *http.Response {
	response := &http.Response{
		StatusCode: statusCode,
		Status:     httpStatusLine(statusCode),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
	if contentType != "" {
		response.Header.Set("Content-Type", contentType)
	}
	return response
}

func httpStatusLine(statusCode int) string {
	return strings.TrimSpace(strings.Join([]string{strconv.Itoa(statusCode), http.StatusText(statusCode)}, " "))
}
