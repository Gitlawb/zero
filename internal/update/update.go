package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepository = "Gitlawb/zero"
	DefaultTimeout    = 5 * time.Second
)

type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

type Result struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	ReleaseURL      string `json:"releaseUrl"`
	TagName         string `json:"tagName"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

// Options configures a release update check.
type Options struct {
	CurrentVersion string
	// Endpoint accepts a full release API URL, an owner/repo slug, or a data:
	// endpoint for deterministic tests.
	Endpoint   string
	Repository string
	Timeout    time.Duration
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

func Check(ctx context.Context, options Options) (Result, error) {
	currentVersion, err := normalizeVersionTag(strings.TrimSpace(firstNonEmpty(options.CurrentVersion, "0.0.0")))
	if err != nil {
		return Result{}, err
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
		UpdateAvailable: compareSemverParts(latestParts, currentParts) > 0,
	}, nil
}

func Format(result Result) string {
	if result.UpdateAvailable {
		return strings.Join([]string{
			fmt.Sprintf("[zero] Update available: %s -> %s", result.CurrentVersion, result.LatestVersion),
			"Release: " + result.ReleaseURL,
			"Download the matching release asset for your platform, then replace the current zero binary.",
		}, "\n")
	}
	return strings.Join([]string{
		fmt.Sprintf("[zero] up to date (%s)", result.CurrentVersion),
		"Latest release: " + result.ReleaseURL,
	}, "\n")
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
