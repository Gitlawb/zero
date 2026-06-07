package providerio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const maxSSELineBytes = 16 * 1024 * 1024

// ErrStreamIdle reports that a streaming upstream stopped sending data without
// closing the connection. Callers surface it as an idle-timeout error.
var ErrStreamIdle = errors.New("idle timeout (upstream stopped sending data)")

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
	return scanSSEPayloads(scanner, handle)
}

// scanSSEPayloads accumulates SSE "data:" lines into payloads (joined across
// continuation lines, flushed on a blank line or EOF) and forwards each to
// handle. It is the shared core of ScanSSEData and the idle-aware variant.
func scanSSEPayloads(scanner *bufio.Scanner, handle func(data string) bool) error {
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

// ScanSSEDataWithContext parses SSE data payloads while enforcing an idle
// timeout and honoring ctx cancellation. The blocking scan runs on a goroutine
// that forwards each completed payload over a buffered channel; this consumer
// selects on ctx.Done, the idle timer, and incoming payloads. When the upstream
// goes silent for idleTimeout, cancel is invoked to abort the in-flight request
// (unblocking the reader) and ErrStreamIdle is returned. On ctx cancellation
// ctx.Err() is returned. A non-positive idleTimeout disables the watchdog.
func ScanSSEDataWithContext(
	ctx context.Context,
	cancel context.CancelFunc,
	reader io.Reader,
	idleTimeout time.Duration,
	handle func(data string) bool,
) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4096), maxSSELineBytes)

	type payload struct {
		data string
	}
	payloads := make(chan payload)
	scanDone := make(chan error, 1)

	go func() {
		scanDone <- scanSSEPayloads(scanner, func(data string) bool {
			select {
			case payloads <- payload{data: data}:
				return true
			case <-ctx.Done():
				return false
			}
		})
		close(payloads)
	}()

	// The idle watchdog is optional. When idleTimeout <= 0 it is disabled, but we
	// STILL run the goroutine + select loop so ctx cancellation is always honored
	// (a nil idleC channel simply never fires in the select).
	var idleC <-chan time.Time
	resetIdle := func() {}
	if idleTimeout > 0 {
		idle := time.NewTimer(idleTimeout)
		defer idle.Stop()
		idleC = idle.C
		resetIdle = func() {
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(idleTimeout)
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Abort the in-flight request so the reader goroutine unblocks and
			// exits on its own; do not wait for it (it may be parked in a read
			// that only the request-context cancel can interrupt).
			cancel()
			return ctx.Err()
		case <-idleC:
			// Upstream went silent without closing. Abort the read and surface
			// a timeout instead of blocking the agent forever.
			cancel()
			return ErrStreamIdle
		case item, ok := <-payloads:
			if !ok {
				// Reader finished: deliver its terminal status (EOF -> nil,
				// scanner error, or ctx cancel observed inside the goroutine).
				if err := <-scanDone; err != nil {
					return err
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				return nil
			}
			resetIdle()
			if !handle(item.data) {
				// The provider asked to stop (e.g. it already emitted an error
				// for this payload). Abort the read and end like ScanSSEData:
				// return nil so callers fall through to their post-scan checks.
				cancel()
				return nil
			}
		}
	}
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
