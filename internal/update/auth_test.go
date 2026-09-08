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
	t              *testing.T
	receivedAuth   string
	responseBody   string
	responseCode   int
	redirectTarget string
}

func (at *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	at.receivedAuth = req.Header.Get("Authorization")
	at.t.Logf("authTransport: %s %s → authorization_present=%t", req.Method, req.URL.String(), at.receivedAuth != "")
	if at.redirectTarget != "" {
		return &http.Response{
			StatusCode: 301,
			Header:     http.Header{"Location": []string{at.redirectTarget}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	if at.responseBody == "" {
		at.responseBody = `{"tag_name":"v0.2.0","html_url":"https://example.test/release","assets":[{"name":"zero-v0.2.0-linux-x64.tar.gz","browser_download_url":"https://example.test/zero-v0.2.0-linux-x64.tar.gz"},{"name":"zero-v0.2.0-linux-x64.tar.gz.sha256","browser_download_url":"https://example.test/zero-v0.2.0-linux-x64.tar.gz.sha256"}]}`
	}
	if at.responseCode == 0 {
		at.responseCode = 200
	}
	return &http.Response{
		StatusCode: at.responseCode,
		Body:       io.NopCloser(strings.NewReader(at.responseBody)),
		Header:     make(http.Header),
	}, nil
}

func TestFetchReleaseSendsAuthToHttpsGithub(t *testing.T) {
	at := &authTransport{t: t}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvUpdateToken, "zero_token")
	t.Setenv(EnvGitHubToken, "github_fallback")

	_, err := fetchRelease(context.Background(), "https://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Fatalf("fetchRelease error: %v", err)
	}
	if at.receivedAuth != "Bearer zero_token" {
		t.Fatalf("ZERO_GITHUB_TOKEN should take precedence: got presence=%t", at.receivedAuth != "")
	}
}

func TestFetchReleaseSendsAuthCaseInsensitive(t *testing.T) {
	at := &authTransport{t: t}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvUpdateToken, "zero_token")
	t.Setenv(EnvGitHubToken, "")

	_, err := fetchRelease(context.Background(), "https://API.GITHUB.COM/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Fatalf("fetchRelease error: %v", err)
	}
	if at.receivedAuth != "Bearer zero_token" {
		t.Fatalf("case-insensitive host should send auth: got presence=%t", at.receivedAuth != "")
	}
}

func TestFetchReleaseFallsBackToGithubToken(t *testing.T) {
	at := &authTransport{t: t}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvGitHubToken, "fallback_token")
	t.Setenv(EnvUpdateToken, "")

	_, err := fetchRelease(context.Background(), "https://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Fatalf("fetchRelease error: %v", err)
	}
	if at.receivedAuth != "Bearer fallback_token" {
		t.Fatalf("GITHUB_TOKEN should be used as fallback: got presence=%t", at.receivedAuth != "")
	}
}

func TestFetchReleaseNoAuthToCustomEndpoint(t *testing.T) {
	at := &authTransport{t: t}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvUpdateToken, "secret")
	t.Setenv(EnvGitHubToken, "fallback")

	_, err := fetchRelease(context.Background(), "https://internal.mirror.example.com/releases/latest")
	if err != nil {
		t.Fatalf("fetchRelease error: %v", err)
	}
	if at.receivedAuth != "" {
		t.Fatalf("auth should not be sent to custom endpoint: got presence=%t", at.receivedAuth != "")
	}
}

func TestFetchReleaseNoAuthToHttpGithub(t *testing.T) {
	at := &authTransport{t: t}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvUpdateToken, "secret")
	t.Setenv(EnvGitHubToken, "")

	_, err := fetchRelease(context.Background(), "http://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Fatalf("fetchRelease error: %v", err)
	}
	if at.receivedAuth != "" {
		t.Fatalf("auth should not be sent over HTTP: got presence=%t", at.receivedAuth != "")
	}
}

func TestFetchReleaseNoAuthWhenTokensNotSet(t *testing.T) {
	at := &authTransport{t: t}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvUpdateToken, "")
	t.Setenv(EnvGitHubToken, "")

	_, err := fetchRelease(context.Background(), "https://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err != nil {
		t.Fatalf("fetchRelease error: %v", err)
	}
	if at.receivedAuth != "" {
		t.Fatalf("no auth should be sent when no tokens set: got presence=%t", at.receivedAuth != "")
	}
}

func TestFetchReleaseRefusesRedirectToHttp(t *testing.T) {
	at := &authTransport{t: t, redirectTarget: "http://api.github.com/repos/Gitlawb/zero/releases/latest"}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvUpdateToken, "secret")
	t.Setenv(EnvGitHubToken, "")

	_, err := fetchRelease(context.Background(), "https://api.github.com/repos/Gitlawb/zero/releases/latest")
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("expected redirect refusal error, got %v", err)
	}
}

func TestFetchReleaseRejectsUserinfoHostTrick(t *testing.T) {
	at := &authTransport{t: t}
	old := httpClient
	httpClient = &http.Client{Transport: at}
	t.Cleanup(func() { httpClient = old })

	t.Setenv(EnvUpdateToken, "secret")
	t.Setenv(EnvGitHubToken, "")

	_, err := fetchRelease(context.Background(), "https://api.github.com@evil.example/releases/latest")
	if err != nil {
		t.Fatalf("fetchRelease error: %v", err)
	}
	if strings.HasPrefix(at.receivedAuth, "Bearer ") {
		t.Fatalf("bearer auth should not be sent when userinfo tricks hostname: got presence=%t", at.receivedAuth != "")
	}
}
