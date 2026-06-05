package update

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeVersionTagAndCompare(t *testing.T) {
	if got := NormalizeVersionTag("v1.2.3+build.4"); got != "1.2.3" {
		t.Fatalf("NormalizeVersionTag = %q, want 1.2.3", got)
	}
	if CompareSemver("0.2.0", "0.1.9") <= 0 {
		t.Fatal("0.2.0 should be newer than 0.1.9")
	}
	if CompareSemver("v0.1.0", "0.1.0") != 0 {
		t.Fatal("v0.1.0 should match 0.1.0")
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

func TestResolveEndpointAcceptsURLAndRepositorySlug(t *testing.T) {
	if got := ResolveEndpoint("Gitlawb/alt-zero", DefaultRepository); got != Endpoint("Gitlawb/alt-zero") {
		t.Fatalf("slug endpoint = %q", got)
	}
	if got := ResolveEndpoint("https://example.test/latest", DefaultRepository); got != "https://example.test/latest" {
		t.Fatalf("URL endpoint = %q", got)
	}
	if got := ResolveEndpoint("", "Gitlawb/fallback"); got != Endpoint("Gitlawb/fallback") {
		t.Fatalf("fallback endpoint = %q", got)
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
}
