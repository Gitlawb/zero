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
		GOOS:           "linux",
		GOARCH:         "amd64",
		Fetch: func(_ context.Context, endpoint string) (Release, error) {
			if endpoint != Endpoint(DefaultRepository) {
				t.Fatalf("endpoint = %q, want default", endpoint)
			}
			return releaseForTarget(t, "v0.2.0", "linux", "amd64"), nil
		},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.UpdateAvailable || result.LatestVersion != "0.2.0" {
		t.Fatalf("unexpected update result: %#v", result)
	}
	if !result.ReleaseAsset.Verified || result.ReleaseAsset.ArchiveName != "zero-v0.2.0-linux-x64.tar.gz" || result.ReleaseAsset.ChecksumName != "zero-v0.2.0-linux-x64.tar.gz.sha256" {
		t.Fatalf("unexpected release asset check: %#v", result.ReleaseAsset)
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
		GOOS:           "linux",
		GOARCH:         "amd64",
		Fetch: func(context.Context, string) (Release, error) {
			release := releaseForTarget(t, "v0.2.0", "linux", "amd64")
			release.HTMLURL = ""
			return release, nil
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
	payload := url.QueryEscape(`{"tag_name":"v0.2.0","html_url":"https://example.test/release","assets":[{"name":"zero-v0.2.0-linux-x64.tar.gz","browser_download_url":"https://example.test/zero-v0.2.0-linux-x64.tar.gz"},{"name":"zero-v0.2.0-linux-x64.tar.gz.sha256","browser_download_url":"https://example.test/zero-v0.2.0-linux-x64.tar.gz.sha256"}]}`)

	result, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		Endpoint:       "data:application/json," + payload,
		GOOS:           "linux",
		GOARCH:         "amd64",
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.UpdateAvailable || result.ReleaseURL != "https://example.test/release" || !result.ReleaseAsset.Verified {
		t.Fatalf("unexpected data endpoint result: %#v", result)
	}
}

func TestCheckRejectsMissingReleaseAssets(t *testing.T) {
	_, err := Check(context.Background(), Options{
		CurrentVersion: "0.1.0",
		GOOS:           "linux",
		GOARCH:         "amd64",
		Fetch: func(context.Context, string) (Release, error) {
			return Release{
				TagName: "v0.2.0",
				Assets: []Asset{
					{Name: "zero-v0.2.0-linux-arm64.tar.gz"},
				},
			}, nil
		},
	})

	if err == nil {
		t.Fatal("Check should reject release metadata without expected assets")
	}
	if !strings.Contains(err.Error(), "zero-v0.2.0-linux-x64.tar.gz") || !strings.Contains(err.Error(), "zero-v0.2.0-linux-x64.tar.gz.sha256") {
		t.Fatalf("Check error = %v, want missing archive and checksum names", err)
	}
}

func TestExpectedAssetCheckUsesInstallerArchiveNames(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		goos        string
		goarch      string
		archiveName string
		platform    string
		arch        string
	}{
		{
			name:        "linux amd64",
			version:     "0.2.0",
			goos:        "linux",
			goarch:      "amd64",
			archiveName: "zero-v0.2.0-linux-x64.tar.gz",
			platform:    "linux",
			arch:        "x64",
		},
		{
			name:        "macos arm64",
			version:     "0.2.0",
			goos:        "darwin",
			goarch:      "arm64",
			archiveName: "zero-v0.2.0-macos-arm64.tar.gz",
			platform:    "macos",
			arch:        "arm64",
		},
		{
			name:        "windows amd64",
			version:     "0.2.0",
			goos:        "windows",
			goarch:      "amd64",
			archiveName: "zero-v0.2.0-windows-x64.zip",
			platform:    "windows",
			arch:        "x64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check, err := expectedAssetCheck(tt.version, tt.goos, tt.goarch)
			if err != nil {
				t.Fatalf("expectedAssetCheck returned error: %v", err)
			}
			if check.ArchiveName != tt.archiveName || check.ChecksumName != tt.archiveName+".sha256" || check.Platform != tt.platform || check.Arch != tt.arch {
				t.Fatalf("expectedAssetCheck = %#v, want archive %s", check, tt.archiveName)
			}
		})
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
		ReleaseAsset:    assetCheckForTest(t, "v0.2.0", "linux", "amd64"),
		UpdateAvailable: true,
	})
	if !strings.Contains(output, "Update available: 0.1.0 -> 0.2.0") {
		t.Fatalf("unexpected update output: %q", output)
	}
	if !strings.Contains(output, "Release asset: zero-v0.2.0-linux-x64.tar.gz") || !strings.Contains(output, "Checksum asset: zero-v0.2.0-linux-x64.tar.gz.sha256") {
		t.Fatalf("update output did not include release assets: %q", output)
	}

	output = Format(Result{
		CurrentVersion:  "0.2.0",
		LatestVersion:   "0.2.0",
		ReleaseURL:      "https://github.com/Gitlawb/zero/releases/tag/v0.2.0",
		TagName:         "v0.2.0",
		ReleaseAsset:    assetCheckForTest(t, "v0.2.0", "linux", "amd64"),
		UpdateAvailable: false,
	})
	if !strings.Contains(output, "up to date") {
		t.Fatalf("unexpected up-to-date output: %q", output)
	}
}

func releaseForTarget(t *testing.T, tag string, goos string, goarch string) Release {
	t.Helper()
	check := assetCheckForTest(t, tag, goos, goarch)
	return Release{
		TagName: tag,
		HTMLURL: "https://github.com/Gitlawb/zero/releases/tag/" + tag,
		Assets: []Asset{
			{Name: check.ArchiveName, BrowserDownloadURL: "https://example.test/" + check.ArchiveName},
			{Name: check.ChecksumName, BrowserDownloadURL: "https://example.test/" + check.ChecksumName},
		},
	}
}

func assetCheckForTest(t *testing.T, tag string, goos string, goarch string) AssetCheck {
	t.Helper()
	version, err := NormalizeVersionTag(tag)
	if err != nil {
		t.Fatalf("NormalizeVersionTag(%q): %v", tag, err)
	}
	check, err := expectedAssetCheck(version, goos, goarch)
	if err != nil {
		t.Fatalf("expectedAssetCheck(%q, %q, %q): %v", version, goos, goarch, err)
	}
	check.ArchiveFound = true
	check.ChecksumFound = true
	check.Verified = true
	return check
}
