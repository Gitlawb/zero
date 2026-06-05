package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNormalizeVersionTagAndCompare(t *testing.T) {
	got, err := NormalizeVersionTag("v1.2.3+build.4")
	if err != nil {
		t.Fatalf("NormalizeVersionTag returned error: %v", err)
	}
	if got != "1.2.3" {
		t.Fatalf("NormalizeVersionTag = %q, want 1.2.3", got)
	}

	comparison, err := CompareSemver("0.2.0", "0.1.9")
	if err != nil {
		t.Fatalf("CompareSemver returned error: %v", err)
	}
	if comparison <= 0 {
		t.Fatal("0.2.0 should be newer than 0.1.9")
	}

	comparison, err = CompareSemver("v0.1.0", "0.1.0")
	if err != nil {
		t.Fatalf("CompareSemver returned error: %v", err)
	}
	if comparison != 0 {
		t.Fatal("v0.1.0 should match 0.1.0")
	}
}

func TestNormalizeVersionTagAndCompareReportInvalidInput(t *testing.T) {
	if _, err := NormalizeVersionTag("nightly"); err == nil {
		t.Fatal("NormalizeVersionTag should reject invalid versions")
	}
	if _, err := NormalizeVersionTag("v999999999999999999999.0.0"); err == nil {
		t.Fatal("NormalizeVersionTag should reject oversized version components")
	}
	if _, err := CompareSemver("0.2.0", "nightly"); err == nil {
		t.Fatal("CompareSemver should reject invalid versions")
	}
	if _, err := CompareSemver("v999999999999999999999.0.0", "0.1.0"); err == nil {
		t.Fatal("CompareSemver should reject oversized version components")
	}
}

func TestCheckReportsAvailableUpdate(t *testing.T) {
	result, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Fetch: func(_ context.Context, endpoint string) (Release, error) {
			if endpoint != Endpoint(DefaultRepository) {
				t.Fatalf("endpoint = %q, want default", endpoint)
			}
			return Release{TagName: "v0.2.0", HTMLURL: "https://github.com/Gitlawb/zero/releases/tag/v0.2.0"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.UpdateAvailable || result.LatestVersion != "0.2.0" {
		t.Fatalf("unexpected update result: %#v", result)
	}
}

func TestCheckReturnsFetchError(t *testing.T) {
	wantErr := errors.New("network failure")

	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Fetch: func(context.Context, string) (Release, error) {
			return Release{}, wantErr
		},
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("Check error = %v, want %v", err, wantErr)
	}
}

func TestCheckRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := Check(ctx, Options{
		CurrentVersion: "0.1.0",
		Fetch: func(ctx context.Context, _ string) (Release, error) {
			<-ctx.Done()
			return Release{}, ctx.Err()
		},
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Check error = %v, want context deadline", err)
	}
}

func TestCheckRejectsNegativeTimeout(t *testing.T) {
	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Timeout:        -time.Second,
		Fetch: func(context.Context, string) (Release, error) {
			t.Fatal("Fetch should not run for invalid timeout")
			return Release{}, nil
		},
	})

	if err == nil || !strings.Contains(err.Error(), "timeout must be non-negative") {
		t.Fatalf("Check error = %v, want non-negative timeout error", err)
	}
}

func TestCheckRejectsMissingTagName(t *testing.T) {
	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Fetch: func(context.Context, string) (Release, error) {
			return Release{HTMLURL: "https://example.test/release"}, nil
		},
	})

	if err == nil || !strings.Contains(err.Error(), "tag_name") {
		t.Fatalf("Check error = %v, want missing tag_name error", err)
	}
}

func TestCheckRejectsInvalidLatestVersion(t *testing.T) {
	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Fetch: func(context.Context, string) (Release, error) {
			return Release{TagName: "nightly"}, nil
		},
	})

	if err == nil || !strings.Contains(err.Error(), "invalid semantic version") {
		t.Fatalf("Check error = %v, want invalid version error", err)
	}
}

func TestCheckFallsBackReleaseURL(t *testing.T) {
	result, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Repository:     "Gitlawb/zero",
		Fetch: func(context.Context, string) (Release, error) {
			return Release{TagName: "v0.2.0"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}

	wantURL := "https://github.com/Gitlawb/zero/releases/tag/v0.2.0"
	if result.ReleaseURL != wantURL {
		t.Fatalf("ReleaseURL = %q, want %q", result.ReleaseURL, wantURL)
	}
}

func TestCheckFetchesDataEndpoint(t *testing.T) {
	payload := url.QueryEscape(`{"tag_name":"v0.2.0","html_url":"https://example.test/release"}`)

	result, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Endpoint:       "data:application/json," + payload,
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.UpdateAvailable || result.ReleaseURL != "https://example.test/release" {
		t.Fatalf("unexpected data endpoint result: %#v", result)
	}
}

func TestCheckReportsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Endpoint:       server.URL,
	})

	if err == nil || !strings.Contains(err.Error(), "github release check failed") {
		t.Fatalf("Check error = %v, want HTTP status error", err)
	}
}

func TestCheckReportsInvalidHTTPJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{"))
	}))
	defer server.Close()

	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Endpoint:       server.URL,
	})

	if err == nil {
		t.Fatal("Check should reject invalid JSON")
	}
}

func TestResolveEndpointAcceptsURLAndRepositorySlug(t *testing.T) {
	got, err := ResolveEndpoint("Gitlawb/alt-zero", DefaultRepository)
	if err != nil {
		t.Fatalf("ResolveEndpoint returned error: %v", err)
	}
	if got != Endpoint("Gitlawb/alt-zero") {
		t.Fatalf("slug endpoint = %q", got)
	}

	got, err = ResolveEndpoint("https://example.test/latest", DefaultRepository)
	if err != nil {
		t.Fatalf("ResolveEndpoint returned error: %v", err)
	}
	if got != "https://example.test/latest" {
		t.Fatalf("URL endpoint = %q", got)
	}

	got, err = ResolveEndpoint("", "Gitlawb/fallback")
	if err != nil {
		t.Fatalf("ResolveEndpoint returned error: %v", err)
	}
	if got != Endpoint("Gitlawb/fallback") {
		t.Fatalf("fallback endpoint = %q", got)
	}
}

func TestResolveEndpointRejectsInvalidInput(t *testing.T) {
	_, err := ResolveEndpoint("not a url", DefaultRepository)

	if err == nil || !strings.Contains(err.Error(), "invalid update endpoint") {
		t.Fatalf("ResolveEndpoint error = %v, want invalid endpoint error", err)
	}
}

func TestFormatResult(t *testing.T) {
	output := Format(Result{
		CurrentVersion:  "0.1.0",
		LatestVersion:   "0.2.0",
		ReleaseURL:      "https://github.com/Gitlawb/zero/releases/tag/v0.2.0",
		TagName:         "v0.2.0",
		UpdateAvailable: true,
	})
	if !strings.Contains(output, "Update available: 0.1.0 -> 0.2.0") {
		t.Fatalf("unexpected update output: %q", output)
	}

	output = Format(Result{
		CurrentVersion:  "0.2.0",
		LatestVersion:   "0.2.0",
		ReleaseURL:      "https://github.com/Gitlawb/zero/releases/tag/v0.2.0",
		TagName:         "v0.2.0",
		UpdateAvailable: false,
	})
	if !strings.Contains(output, "up to date") {
		t.Fatalf("unexpected up-to-date output: %q", output)
	}
}
