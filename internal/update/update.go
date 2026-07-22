package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepository = "Gitlawb/zero"
	DefaultTimeout    = 5 * time.Second
)

type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url,omitempty"`
}

type Result struct {
	CurrentVersion  string     `json:"currentVersion"`
	LatestVersion   string     `json:"latestVersion"`
	ReleaseURL      string     `json:"releaseUrl"`
	TagName         string     `json:"tagName"`
	ReleaseAsset    AssetCheck `json:"releaseAsset"`
	UpdateAvailable bool       `json:"updateAvailable"`
}

type AssetCheck struct {
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	ArchiveName   string `json:"archiveName"`
	ArchiveURL    string `json:"archiveUrl,omitempty"`
	ChecksumName  string `json:"checksumName"`
	ChecksumURL   string `json:"checksumUrl,omitempty"`
	ArchiveFound  bool   `json:"archiveFound"`
	ChecksumFound bool   `json:"checksumFound"`
	Verified      bool   `json:"verified"`
}

// Target identifies a supported release archive target.
type Target struct {
	Name     string `json:"name"`
	GOOS     string `json:"goos"`
	GOARCH   string `json:"goarch"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
}

// Options configures a release update check.
type Options struct {
	CurrentVersion string
	// Endpoint accepts a full release API URL, an owner/repo slug, or a data:
	// endpoint for deterministic tests.
	Endpoint   string
	Repository string
	Timeout    time.Duration
	GOOS       string
	GOARCH     string
	// Fetch overrides the release fetcher for tests and alternate transports.
	Fetch func(context.Context, string) (Release, error)
}

type semverParts [3]int

var (
	versionPattern    = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// Endpoint returns the GitHub latest-release API endpoint for a repository.
func Endpoint(repository string) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repository)
}

// ResolveEndpoint resolves a URL or owner/repo slug into a release API endpoint.
func ResolveEndpoint(endpointOrRepository string, repository string) (string, error) {
	return resolveEndpoint(endpointOrRepository, repository)
}

// NormalizeVersionTag returns a comparable x.y.z version from a release tag.
func NormalizeVersionTag(version string) (string, error) {
	return normalizeVersionTag(version)
}

// CompareSemver compares two semver-ish release tags.
func CompareSemver(left string, right string) (int, error) {
	leftParts, err := parseSemver(left)
	if err != nil {
		return 0, err
	}
	rightParts, err := parseSemver(right)
	if err != nil {
		return 0, err
	}
	return compareSemverParts(leftParts, rightParts), nil
}

// ResolveTarget maps a release target name like windows-x64 to Go build
// coordinates and release asset naming fields.
func ResolveTarget(target string) (Target, error) {
	value := strings.TrimSpace(strings.ToLower(target))
	switch value {
	case "linux-x64":
		return Target{Name: value, GOOS: "linux", GOARCH: "amd64", Platform: "linux", Arch: "x64"}, nil
	case "linux-arm64":
		return Target{Name: value, GOOS: "linux", GOARCH: "arm64", Platform: "linux", Arch: "arm64"}, nil
	case "macos-x64":
		return Target{Name: value, GOOS: "darwin", GOARCH: "amd64", Platform: "macos", Arch: "x64"}, nil
	case "macos-arm64":
		return Target{Name: value, GOOS: "darwin", GOARCH: "arm64", Platform: "macos", Arch: "arm64"}, nil
	case "windows-x64":
		return Target{Name: value, GOOS: "windows", GOARCH: "amd64", Platform: "windows", Arch: "x64"}, nil
	case "windows-arm64":
		return Target{Name: value, GOOS: "windows", GOARCH: "arm64", Platform: "windows", Arch: "arm64"}, nil
	default:
		return Target{}, fmt.Errorf("unsupported update target %q: expected one of linux-x64, linux-arm64, macos-x64, macos-arm64, windows-x64, windows-arm64", target)
	}
}

func Check(ctx context.Context, options Options) (Result, error) {
	rawVersion := strings.TrimSpace(firstNonEmpty(options.CurrentVersion, "0.0.0"))
	currentVersion, err := normalizeVersionTag(rawVersion)
	if err != nil {
		// Source/dev builds carry a non-semver version ("dev"); the check is
		// still useful there — compare as 0.0.0 so the latest release is always
		// reported as available instead of failing before the network call.
		currentVersion = "0.0.0"
	}
	repository := strings.TrimSpace(firstNonEmpty(options.Repository, DefaultRepository))
	endpoint, err := resolveEndpoint(firstNonEmpty(options.Endpoint, os.Getenv("ZERO_UPDATE_RELEASE_URL")), repository)
	if err != nil {
		return Result{}, err
	}
	timeout := options.Timeout
	if timeout < 0 {
		return Result{}, fmt.Errorf("timeout must be non-negative")
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	fetch := options.Fetch
	if fetch == nil {
		fetch = fetchRelease
	}
	release, err := fetch(ctx, endpoint)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(release.TagName) == "" {
		return Result{}, fmt.Errorf("github release response did not include a tag_name")
	}
	latestVersion, err := normalizeVersionTag(release.TagName)
	if err != nil {
		return Result{}, err
	}
	releaseURL := strings.TrimSpace(release.HTMLURL)
	if releaseURL == "" {
		releaseURL = fmt.Sprintf("https://github.com/%s/releases/tag/%s", repository, release.TagName)
	}
	assetCheck, err := verifyReleaseAssets(release, latestVersion, options)
	if err != nil {
		return Result{}, err
	}
	latestParts, err := parseSemverNormalized(latestVersion)
	if err != nil {
		return Result{}, err
	}
	currentParts, err := parseSemverNormalized(currentVersion)
	if err != nil {
		return Result{}, err
	}
	return Result{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		ReleaseURL:      releaseURL,
		TagName:         release.TagName,
		ReleaseAsset:    assetCheck,
		UpdateAvailable: compareSemverParts(latestParts, currentParts) > 0,
	}, nil
}

func Format(result Result) string {
	if result.UpdateAvailable {
		lines := []string{
			fmt.Sprintf("[zero] Update available: %s -> %s", result.CurrentVersion, result.LatestVersion),
			"Release: " + result.ReleaseURL,
		}
		lines = appendAssetLines(lines, result.ReleaseAsset)
		if target := releaseAssetTarget(result.ReleaseAsset); target != "" {
			lines = append(lines, "Download the verified "+target+" release asset, then replace the current zero binary.")
		} else {
			lines = append(lines, "Download the verified release asset, then replace the current zero binary.")
		}
		return strings.Join(lines, "\n")
	}
	lines := []string{
		fmt.Sprintf("[zero] up to date (%s)", result.CurrentVersion),
		"Latest release: " + result.ReleaseURL,
	}
	lines = appendAssetLines(lines, result.ReleaseAsset)
	return strings.Join(lines, "\n")
}

func appendAssetLines(lines []string, asset AssetCheck) []string {
	if asset.ArchiveName == "" {
		return lines
	}
	if target := releaseAssetTarget(asset); target != "" {
		lines = append(lines, "Release target: "+target)
	}
	lines = append(lines, "Release asset: "+asset.ArchiveName)
	if asset.ChecksumName != "" {
		lines = append(lines, "Checksum asset: "+asset.ChecksumName)
	}
	return lines
}

func releaseAssetTarget(asset AssetCheck) string {
	if asset.Platform == "" || asset.Arch == "" {
		return ""
	}
	return asset.Platform + "-" + asset.Arch
}

func fetchRelease(ctx context.Context, endpoint string) (release Release, err error) {
	if strings.HasPrefix(endpoint, "data:") {
		return fetchDataRelease(endpoint)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "zero/update")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Release{}, err
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close update response: %w", closeErr)
		}
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Release{}, fmt.Errorf("github release check failed (%s)", response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func fetchDataRelease(endpoint string) (Release, error) {
	comma := strings.Index(endpoint, ",")
	if comma == -1 {
		return Release{}, fmt.Errorf("invalid data update endpoint")
	}
	payload, err := url.QueryUnescape(endpoint[comma+1:])
	if err != nil {
		return Release{}, err
	}
	var release Release
	if err := json.Unmarshal([]byte(payload), &release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func resolveEndpoint(endpointOrRepository string, repository string) (string, error) {
	value := strings.TrimSpace(endpointOrRepository)
	if value == "" {
		return Endpoint(repository), nil
	}
	if repositoryPattern.MatchString(value) {
		return Endpoint(value), nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("invalid update endpoint %q: use a full URL or an owner/repo slug like %s", value, repository)
	}
	return value, nil
}

func verifyReleaseAssets(release Release, version string, options Options) (AssetCheck, error) {
	assetCheck, err := expectedAssetCheck(version, options.GOOS, options.GOARCH)
	if err != nil {
		return AssetCheck{}, err
	}
	for _, asset := range release.Assets {
		name := strings.TrimSpace(asset.Name)
		switch name {
		case assetCheck.ArchiveName:
			assetCheck.ArchiveFound = true
			assetCheck.ArchiveURL = strings.TrimSpace(asset.BrowserDownloadURL)
		case assetCheck.ChecksumName:
			assetCheck.ChecksumFound = true
			assetCheck.ChecksumURL = strings.TrimSpace(asset.BrowserDownloadURL)
		}
	}
	assetCheck.Verified = assetCheck.ArchiveFound && assetCheck.ChecksumFound
	if assetCheck.Verified {
		return assetCheck, nil
	}
	missing := []string{}
	if !assetCheck.ArchiveFound {
		missing = append(missing, assetCheck.ArchiveName)
	}
	if !assetCheck.ChecksumFound {
		missing = append(missing, assetCheck.ChecksumName)
	}
	return AssetCheck{}, fmt.Errorf("release metadata missing expected asset(s) for %s-%s: %s", assetCheck.Platform, assetCheck.Arch, strings.Join(missing, ", "))
}

func expectedAssetCheck(version string, goos string, goarch string) (AssetCheck, error) {
	platform, err := releasePlatform(firstNonEmpty(goos, runtime.GOOS))
	if err != nil {
		return AssetCheck{}, err
	}
	arch, err := releaseArch(firstNonEmpty(goarch, runtime.GOARCH))
	if err != nil {
		return AssetCheck{}, err
	}
	extension := "tar.gz"
	if platform == "windows" {
		extension = "zip"
	}
	archiveName := fmt.Sprintf("zero-v%s-%s-%s.%s", version, platform, arch, extension)
	return AssetCheck{
		Platform:     platform,
		Arch:         arch,
		ArchiveName:  archiveName,
		ChecksumName: archiveName + ".sha256",
	}, nil
}

func releasePlatform(goos string) (string, error) {
	switch strings.TrimSpace(goos) {
	case "linux":
		return "linux", nil
	case "darwin":
		return "macos", nil
	case "windows":
		return "windows", nil
	default:
		// No prebuilt release archive is published for this GOOS (e.g.
		// Android/Termux). This does not mean the platform is unsupported --
		// Termux runs zero fine via the npm wrapper -- it just has no
		// self-updating release asset. Point users at `npm update` rather
		// than a source rebuild, since that's the documented Termux
		// install/upgrade path and doesn't require a Go toolchain.
		return "", fmt.Errorf("no published release for %q (release assets: linux, macos, windows). Your build is the current version of record. Upgrade with `npm update -g @gitlawb/zero` to get the latest.", goos) //nolint:staticcheck // Preserve established user-facing error text.
	}
}

func releaseArch(goarch string) (string, error) {
	switch strings.TrimSpace(goarch) {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported release architecture: %s", goarch)
	}
}

func normalizeVersionTag(version string) (string, error) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return "", fmt.Errorf("invalid semantic version: %s", version)
	}
	major, err := parseVersionComponent(version, match[1])
	if err != nil {
		return "", err
	}
	minor, err := parseVersionComponent(version, match[2])
	if err != nil {
		return "", err
	}
	patch, err := parseVersionComponent(version, match[3])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

func parseSemver(version string) (semverParts, error) {
	normalized, err := NormalizeVersionTag(version)
	if err != nil {
		return semverParts{}, err
	}
	return parseSemverNormalized(normalized)
}

func parseSemverNormalized(version string) (semverParts, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return semverParts{}, fmt.Errorf("invalid semantic version: %s", version)
	}
	major, err := parseVersionComponent(version, parts[0])
	if err != nil {
		return semverParts{}, err
	}
	minor, err := parseVersionComponent(version, parts[1])
	if err != nil {
		return semverParts{}, err
	}
	patch, err := parseVersionComponent(version, parts[2])
	if err != nil {
		return semverParts{}, err
	}
	return semverParts{major, minor, patch}, nil
}

func compareSemverParts(left semverParts, right semverParts) int {
	for index := range left {
		if left[index] != right[index] {
			return left[index] - right[index]
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func parseVersionComponent(version string, component string) (int, error) {
	parsed, err := strconv.ParseInt(component, 10, 31)
	if err != nil {
		return 0, fmt.Errorf("invalid semantic version: %s", version)
	}
	return int(parsed), nil
}
