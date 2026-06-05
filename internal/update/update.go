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

type Options struct {
	CurrentVersion string
	Endpoint       string
	Repository     string
	Timeout        time.Duration
	Fetch          func(context.Context, string) (Release, error)
}

type semverParts [3]int

var (
	versionPattern    = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

func Endpoint(repository string) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repository)
}

func ResolveEndpoint(endpointOrRepository string, repository string) string {
	value := strings.TrimSpace(endpointOrRepository)
	if value == "" {
		return Endpoint(repository)
	}
	if repositoryPattern.MatchString(value) {
		return Endpoint(value)
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" {
		panic(fmt.Sprintf("Invalid update endpoint %q. Use a full URL or an owner/repo slug like %s.", value, repository))
	}
	return value
}

func NormalizeVersionTag(version string) string {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		panic(fmt.Sprintf("Invalid semantic version: %s", version))
	}
	return fmt.Sprintf("%d.%d.%d", atoi(match[1]), atoi(match[2]), atoi(match[3]))
}

func CompareSemver(left string, right string) int {
	leftParts := parseSemver(left)
	rightParts := parseSemver(right)
	for index := range leftParts {
		if leftParts[index] != rightParts[index] {
			return leftParts[index] - rightParts[index]
		}
	}
	return 0
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
		return Result{}, fmt.Errorf("GitHub release response did not include a tag_name")
	}
	latestVersion, err := normalizeVersionTag(release.TagName)
	if err != nil {
		return Result{}, err
	}
	releaseURL := strings.TrimSpace(release.HTMLURL)
	if releaseURL == "" {
		releaseURL = fmt.Sprintf("https://github.com/%s/releases/tag/%s", repository, release.TagName)
	}
	return Result{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		ReleaseURL:      releaseURL,
		TagName:         release.TagName,
		UpdateAvailable: compareSemverParts(parseSemverNormalized(latestVersion), parseSemverNormalized(currentVersion)) > 0,
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

func fetchRelease(ctx context.Context, endpoint string) (Release, error) {
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
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Release{}, fmt.Errorf("GitHub release check failed (%s)", response.Status)
	}
	var release Release
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
		return "", fmt.Errorf("Invalid update endpoint %q. Use a full URL or an owner/repo slug like %s.", value, repository)
	}
	return value, nil
}

func normalizeVersionTag(version string) (string, error) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return "", fmt.Errorf("Invalid semantic version: %s", version)
	}
	return fmt.Sprintf("%d.%d.%d", atoi(match[1]), atoi(match[2]), atoi(match[3])), nil
}

func parseSemver(version string) semverParts {
	normalized := NormalizeVersionTag(version)
	return parseSemverNormalized(normalized)
}

func parseSemverNormalized(version string) semverParts {
	parts := strings.Split(version, ".")
	return semverParts{atoi(parts[0]), atoi(parts[1]), atoi(parts[2])}
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

func atoi(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		panic(err)
	}
	return parsed
}
