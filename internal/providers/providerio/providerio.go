package providerio

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const maxSSELineBytes = 16 * 1024 * 1024

// NormalizeBaseURL trims trailing slashes and validates an HTTP API base URL.
func NormalizeBaseURL(baseURL string, defaultBaseURL string, label string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return "", fmt.Errorf("invalid %s base URL: %w", label, err)
	}
	return baseURL, nil
}

// HTTPClient returns the configured client or the process default.
func HTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

// SendEvent writes a provider event without blocking cancellation cleanup.
func SendEvent(ctx context.Context, events chan<- zeroruntime.StreamEvent, event zeroruntime.StreamEvent) {
	select {
	case <-ctx.Done():
		if event.Type == zeroruntime.StreamEventError {
			select {
			case events <- event:
			default:
			}
		}
	case events <- event:
	}
}

// ScanSSEData parses Server-Sent Event data fields from a streaming response.
func ScanSSEData(reader io.Reader, handle func(data string) bool) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4096), maxSSELineBytes)

	dataLines := []string{}
	flush := func() bool {
		if len(dataLines) == 0 {
			return true
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if data == "" || data == "[DONE]" {
			return true
		}
		return handle(data)
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if !flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimLeft(strings.TrimPrefix(line, "data:"), " \t"))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

// ClassifiedError normalizes provider HTTP/stream errors and redacts secrets.
func ClassifiedError(statusCode int, message string, apiKey string) string {
	prefix := "provider error: "
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		prefix = "auth error: "
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, 529:
		prefix = "rate limit error: "
	default:
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			prefix = "provider request error: "
		}
	}
	return Redact(prefix+message, apiKey)
}

// Redact removes known API-key and bearer-token forms from provider messages.
func Redact(message string, apiKey string) string {
	if apiKey != "" {
		message = strings.ReplaceAll(message, apiKey, "[REDACTED]")
	}
	words := strings.Fields(message)
	for index := 0; index < len(words)-1; index++ {
		if strings.EqualFold(strings.TrimRight(words[index], ":"), "Bearer") {
			words[index] = "authorization"
			words[index+1] = "[REDACTED]"
		}
	}
	return strings.Join(words, " ")
}

// PositiveOrDefault validates optional max token settings.
func PositiveOrDefault(value int, fallback int, label string) (int, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must be a positive integer", label)
	}
	return value, nil
}
