package update

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// authTransport intercepts HTTP requests and records the Authorization header
// so tests can verify whether fetchRelease sent credentials.
type authTransport struct {
	t             *testing.T
	receivedAuth  string
	responseBody  string
	responseCode  int
}

func (at *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	at.receivedAuth = req.Header.Get("Authorization")
	at.t.Logf("authTransport: %s %s → Authorization: %q", req.Method, req.URL.String(), at.receivedAuth)
	if at.responseBody == "" {
		at.responseBody = `{"tag_name":"v0.2.0","html_url":"https://example.test/release","assets":[{"name":"zero-v0.2.0-linux-x64.tar.gz","browser_download_url":"https://example.test/zero-v0.2.0-linux-x64.tar.gz"},{"name":"zero-v0.2.0-linux-x64.tar.gz.sha256","browser_download_url":"https://example.test/zero-v0.2.0-linux-x64.tar.gz.sha256"}]}`
	}
	if at.responseCode == 0 {
		at.responseCode = 200
	}
	return &http.Response{
		StatusCode:    at.responseCode,
		Body:          io.NopCloser(strings.NewReader(at.responseBody)),
		Header:        make(http.Header),
	}, nil
}

func TestFetchReleaseSendsAuthToHttpsGithub(t *testing.T) {
	// ZERO_GITHUB_TOKEN → sent to https://api.github.com
	at := &authTransport{t: t}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: at}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("ZERO_GITHUB_TOKEN", "zero_token")
	t.Setenv("GITHUB_TOKEN", "github_fallback")

	_, err := fetchRelease(context.Background(), "https://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Logf("fetchRelease returned error (expected for fake response): %v", err)
	}
	if at.receivedAuth != "Bearer zero_token" {
		t.Fatalf("ZERO_GITHUB_TOKEN should take precedence: got Authorization %q, want %q", at.receivedAuth, "Bearer zero_token")
	}
}

func TestFetchReleaseFallsBackToGithubToken(t *testing.T) {
	// Only GITHUB_TOKEN set → sent to https://api.github.com
	at := &authTransport{t: t}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: at}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("GITHUB_TOKEN", "fallback_token")
	t.Setenv("ZERO_GITHUB_TOKEN", "") // clear ambient precedence var

	_, err := fetchRelease(context.Background(), "https://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Logf("fetchRelease returned error (expected for fake response): %v", err)
	}
	if at.receivedAuth != "Bearer fallback_token" {
		t.Fatalf("GITHUB_TOKEN should be used as fallback: got Authorization %q, want %q", at.receivedAuth, "Bearer fallback_token")
	}
}

func TestFetchReleaseNoAuthToCustomEndpoint(t *testing.T) {
	// Token set but endpoint is custom host → no auth sent
	at := &authTransport{t: t}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: at}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("ZERO_GITHUB_TOKEN", "secret")
	t.Setenv("GITHUB_TOKEN", "fallback")

	_, err := fetchRelease(context.Background(), "https://internal.mirror.example.com/releases/latest")
	if err != nil {
		t.Logf("fetchRelease returned error (expected for fake response): %v", err)
	}
	if at.receivedAuth != "" {
		t.Fatalf("auth should not be sent to custom endpoint: got Authorization %q", at.receivedAuth)
	}
}

func TestFetchReleaseNoAuthToHttpGithub(t *testing.T) {
	// Token set but scheme is HTTP → no auth sent even to api.github.com
	at := &authTransport{t: t}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: at}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("ZERO_GITHUB_TOKEN", "secret")

	_, err := fetchRelease(context.Background(), "http://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Logf("fetchRelease returned error (expected for fake response): %v", err)
	}
	if at.receivedAuth != "" {
		t.Fatalf("auth should not be sent over HTTP: got Authorization %q", at.receivedAuth)
	}
}

func TestFetchReleaseNoAuthWhenTokensNotSet(t *testing.T) {
	// No env vars set → no auth sent (authenticated fallback)
	at := &authTransport{t: t}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: at}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("ZERO_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	_, err := fetchRelease(context.Background(), "https://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Logf("fetchRelease returned error (expected for fake response): %v", err)
	}
	if at.receivedAuth != "" {
		t.Fatalf("no auth should be sent when no tokens set: got Authorization %q", at.receivedAuth)
	}
}
