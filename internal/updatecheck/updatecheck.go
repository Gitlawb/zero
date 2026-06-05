package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepository         = "Gitlawb/zero"
	DefaultUpdateCheckTimeout = 5 * time.Second
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	CurrentVersion string
	Endpoint       string
	Repository     string
	Timeout        time.Duration
	Client         HTTPClient
}

type Result struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	ReleaseURL      string `json:"releaseUrl"`
	TagName         string `json:"tagName"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

type endpointResolution struct {
	URL        string
	Repository string
}

type githubReleaseResponse struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

var versionTagPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)
var repositorySlugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func ReleaseEndpoint(repository string) string {
	return "https://api.github.com/repos/" + repository + "/releases/latest"
}

func ResolveReleaseEndpoint(endpointOrRepository string, repository string) (string, error) {
	resolved, err := resolveReleaseEndpoint(endpointOrRepository, repository)
	if err != nil {
		return "", err
	}
	return resolved.URL, nil
}

func NormalizeVersionTag(version string) (string, error) {
	trimmed := strings.TrimSpace(version)
	matches := versionTagPattern.FindStringSubmatch(trimmed)
	if matches == nil {
		return "", fmt.Errorf("invalid semantic version: %s", version)
	}

	major, err := parseSemverComponent(matches[1], version)
	if err != nil {
		return "", err
	}
	minor, err := parseSemverComponent(matches[2], version)
	if err != nil {
		return "", err
	}
	patch, err := parseSemverComponent(matches[3], version)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

func CompareSemver(left string, right string) (int, error) {
	leftParts, err := parseSemver(left)
	if err != nil {
		return 0, err
	}
	rightParts, err := parseSemver(right)
	if err != nil {
		return 0, err
	}

	for index := range leftParts {
		if leftParts[index] > rightParts[index] {
			return 1, nil
		}
		if leftParts[index] < rightParts[index] {
			return -1, nil
		}
	}
	return 0, nil
}

func Check(ctx context.Context, options Options) (Result, error) {
	currentVersion, err := NormalizeVersionTag(options.CurrentVersion)
	if err != nil {
		return Result{}, err
	}

	repository := strings.TrimSpace(options.Repository)
	if repository == "" {
		repository = DefaultRepository
	}

	endpointValue := options.Endpoint
	if strings.TrimSpace(endpointValue) == "" {
		endpointValue = os.Getenv("ZERO_UPDATE_RELEASE_URL")
	}
	resolvedEndpoint, err := resolveReleaseEndpoint(endpointValue, repository)
	if err != nil {
		return Result{}, err
	}

	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultUpdateCheckTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedEndpoint.URL, nil)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "zero/"+currentVersion)

	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}

	response, err := client.Do(request)
	if err != nil {
		return Result{}, err
	}

	if response.StatusCode < 200 || response.StatusCode > 299 {
		status := response.Status
		if strings.TrimSpace(status) == "" {
			status = strconv.Itoa(response.StatusCode)
		}
		if err := response.Body.Close(); err != nil {
			return Result{}, fmt.Errorf("close release response body: %w", err)
		}
		return Result{}, fmt.Errorf("GitHub release check failed (%s)", status)
	}

	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return Result{}, readErr
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close release response body: %w", closeErr)
	}

	var release githubReleaseResponse
	if err := json.Unmarshal(body, &release); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(release.TagName) == "" {
		return Result{}, errors.New("GitHub release response did not include a tag_name")
	}

	latestVersion, err := NormalizeVersionTag(release.TagName)
	if err != nil {
		return Result{}, err
	}
	comparison, err := CompareSemver(latestVersion, currentVersion)
	if err != nil {
		return Result{}, err
	}

	releaseURL := strings.TrimSpace(release.HTMLURL)
	if releaseURL == "" {
		releaseURL = fmt.Sprintf("https://github.com/%s/releases/tag/%s", resolvedEndpoint.Repository, release.TagName)
	}

	return Result{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		ReleaseURL:      releaseURL,
		TagName:         release.TagName,
		UpdateAvailable: comparison > 0,
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

func resolveReleaseEndpoint(endpointOrRepository string, repository string) (endpointResolution, error) {
	value := strings.TrimSpace(endpointOrRepository)
	if value == "" {
		return endpointResolution{URL: ReleaseEndpoint(repository), Repository: repository}, nil
	}

	if repositorySlugPattern.MatchString(value) {
		return endpointResolution{URL: ReleaseEndpoint(value), Repository: value}, nil
	}

	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return endpointResolution{}, fmt.Errorf("invalid update endpoint %q. Use a full URL or an owner/repo slug like %s", value, repository)
	}
	return endpointResolution{URL: value, Repository: repository}, nil
}

func parseSemver(version string) ([3]uint64, error) {
	normalized, err := NormalizeVersionTag(version)
	if err != nil {
		return [3]uint64{}, err
	}

	parts := strings.Split(normalized, ".")
	parsed := [3]uint64{}
	for index, part := range parts {
		value, err := parseSemverComponent(part, version)
		if err != nil {
			return [3]uint64{}, err
		}
		parsed[index] = value
	}
	return parsed, nil
}

func parseSemverComponent(component string, originalVersion string) (uint64, error) {
	value, err := strconv.ParseUint(component, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid semantic version: %s", originalVersion)
	}
	return value, nil
}
