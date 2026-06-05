package updatecheck

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripClient struct {
	requests []*http.Request
	response *http.Response
	err      error
}

func (client *roundTripClient) Do(request *http.Request) (*http.Response, error) {
	client.requests = append(client.requests, request)
	if client.err != nil {
		return nil, client.err
	}
	return client.response, nil
}

func releaseResponse(statusCode int, status string, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNormalizeVersionTag(t *testing.T) {
	tests := map[string]string{
		"v0.2.0":         "0.2.0",
		"0.2.0":          "0.2.0",
		"v1.2.3+build.4": "1.2.3",
		"v01.002.0003-a": "1.2.3",
	}

	for input, want := range tests {
		got, err := NormalizeVersionTag(input)
		if err != nil {
			t.Fatalf("NormalizeVersionTag(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeVersionTag(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeVersionTagRejectsOverflowingComponents(t *testing.T) {
	_, err := NormalizeVersionTag("v999999999999999999999999999999999999.0.0")
	if err == nil || !strings.Contains(err.Error(), "invalid semantic version") {
		t.Fatalf("expected overflow to be rejected as an invalid semantic version, got %v", err)
	}
}

func TestCompareSemver(t *testing.T) {
	assertComparison(t, "0.2.0", "0.1.9", 1)
	assertComparison(t, "1.0.0", "0.99.99", 1)
	assertComparison(t, "0.1.1", "0.1.2", -1)
	assertComparison(t, "v0.1.0", "0.1.0", 0)
}

func TestCheckReportsAvailableUpdate(t *testing.T) {
	client := &roundTripClient{response: releaseResponse(200, "200 OK", `{
		"tag_name": "v0.2.0",
		"html_url": "https://github.com/Gitlawb/zero/releases/tag/v0.2.0"
	}`)}

	result, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Client:         client,
		Timeout:        -1,
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}

	if result.CurrentVersion != "0.1.0" || result.LatestVersion != "0.2.0" || result.TagName != "v0.2.0" {
		t.Fatalf("unexpected result versions: %#v", result)
	}
	if result.ReleaseURL != "https://github.com/Gitlawb/zero/releases/tag/v0.2.0" {
		t.Fatalf("ReleaseURL = %q", result.ReleaseURL)
	}
	if !result.UpdateAvailable {
		t.Fatal("expected update to be available")
	}

	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	request := client.requests[0]
	if request.URL.String() != ReleaseEndpoint(DefaultRepository) {
		t.Fatalf("request URL = %q, want default release endpoint", request.URL.String())
	}
	if got := request.Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := request.Header.Get("User-Agent"); got != "zero/0.1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestCheckReportsUpToDate(t *testing.T) {
	client := &roundTripClient{response: releaseResponse(200, "200 OK", `{
		"tag_name": "v0.2.0",
		"html_url": "https://github.com/Gitlawb/zero/releases/tag/v0.2.0"
	}`)}

	result, err := Check(context.Background(), Options{
		CurrentVersion: "0.2.0",
		Client:         client,
		Timeout:        -1,
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if result.UpdateAvailable {
		t.Fatal("expected up-to-date result")
	}
}

func TestCheckThrowsOnMalformedReleasePayloads(t *testing.T) {
	client := &roundTripClient{response: releaseResponse(200, "200 OK", `{"name":"Zero 0.2.0"}`)}

	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Client:         client,
		Timeout:        -1,
	})
	if err == nil || !strings.Contains(err.Error(), "tag_name") {
		t.Fatalf("expected tag_name error, got %v", err)
	}
}

func TestCheckAppliesTimeoutContext(t *testing.T) {
	client := &roundTripClient{response: releaseResponse(200, "200 OK", `{"tag_name":"v0.1.0"}`)}

	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Client:         client,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	if _, ok := client.requests[0].Context().Deadline(); !ok {
		t.Fatal("expected request context to have a timeout deadline")
	}
}

func TestCheckResolvesEndpointPrecedenceAndRepositorySlugs(t *testing.T) {
	client := &roundTripClient{response: releaseResponse(200, "200 OK", `{"tag_name":"v0.1.0"}`)}
	t.Setenv("ZERO_UPDATE_RELEASE_URL", "Gitlawb/env-zero")

	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Endpoint:       "Gitlawb/option-zero",
		Client:         client,
		Timeout:        -1,
	})
	if err != nil {
		t.Fatalf("Check with endpoint option returned error: %v", err)
	}
	if got := client.requests[len(client.requests)-1].URL.String(); got != ReleaseEndpoint("Gitlawb/option-zero") {
		t.Fatalf("endpoint option URL = %q", got)
	}

	client.response = releaseResponse(200, "200 OK", `{"tag_name":"v0.1.0"}`)
	_, err = Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Repository:     "Gitlawb/repo-zero",
		Client:         client,
		Timeout:        -1,
	})
	if err != nil {
		t.Fatalf("Check with env endpoint returned error: %v", err)
	}
	if got := client.requests[len(client.requests)-1].URL.String(); got != ReleaseEndpoint("Gitlawb/env-zero") {
		t.Fatalf("env endpoint URL = %q", got)
	}

	t.Setenv("ZERO_UPDATE_RELEASE_URL", "")
	client.response = releaseResponse(200, "200 OK", `{"tag_name":"v0.1.0"}`)
	_, err = Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Repository:     "Gitlawb/repo-zero",
		Client:         client,
		Timeout:        -1,
	})
	if err != nil {
		t.Fatalf("Check with default repository returned error: %v", err)
	}
	if got := client.requests[len(client.requests)-1].URL.String(); got != ReleaseEndpoint("Gitlawb/repo-zero") {
		t.Fatalf("repository endpoint URL = %q", got)
	}
}

func TestResolveReleaseEndpointAcceptsFullURLsAndRejectsInvalidValues(t *testing.T) {
	got, err := ResolveReleaseEndpoint("https://example.test/latest", "Gitlawb/zero")
	if err != nil {
		t.Fatalf("ResolveReleaseEndpoint full URL returned error: %v", err)
	}
	if got != "https://example.test/latest" {
		t.Fatalf("full URL = %q", got)
	}

	got, err = ResolveReleaseEndpoint("Gitlawb/zero", "Fallback/repo")
	if err != nil {
		t.Fatalf("ResolveReleaseEndpoint slug returned error: %v", err)
	}
	if got != ReleaseEndpoint("Gitlawb/zero") {
		t.Fatalf("slug URL = %q", got)
	}

	_, err = ResolveReleaseEndpoint("not-a-url", "Gitlawb/zero")
	if err == nil || !strings.Contains(err.Error(), "invalid update endpoint") {
		t.Fatalf("expected invalid endpoint error, got %v", err)
	}
}

func TestFormat(t *testing.T) {
	output := Format(Result{
		CurrentVersion:  "0.1.0",
		LatestVersion:   "0.2.0",
		ReleaseURL:      "https://github.com/Gitlawb/zero/releases/tag/v0.2.0",
		TagName:         "v0.2.0",
		UpdateAvailable: true,
	})

	if !strings.Contains(output, "Update available: 0.1.0 -> 0.2.0") {
		t.Fatalf("expected update text, got %q", output)
	}
}

func assertComparison(t *testing.T, left string, right string, want int) {
	t.Helper()
	got, err := CompareSemver(left, right)
	if err != nil {
		t.Fatalf("CompareSemver(%q, %q) returned error: %v", left, right, err)
	}
	if got != want {
		t.Fatalf("CompareSemver(%q, %q) = %d, want %d", left, right, got, want)
	}
}
