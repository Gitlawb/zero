package providerio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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
	return scanSSEPayloads(scanner, handle, nil)
}

// scanSSEPayloads accumulates SSE "data:" lines into payloads (joined across
// continuation lines, flushed on a blank line or EOF) and forwards each to
// handle. It is the shared core of ScanSSEData and the idle-aware variant.
// onComment (optional) fires for ":"-prefixed comment lines — SSE keep-alive
// heartbeats (e.g. OpenRouter's ": OPENROUTER PROCESSING") that carry no data
// but prove the upstream is alive; returning false stops the scan.
func scanSSEPayloads(scanner *bufio.Scanner, handle func(data string) bool, onComment func() bool) error {
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
			if onComment != nil && !onComment() {
				return nil
			}
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
		data      string
		keepAlive bool
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
		}, func() bool {
			// Comment keep-alives carry no payload but must feed the idle
			// watchdog: a heartbeating upstream is NOT idle, and aborting it
			// killed healthy long-running requests. The marker is forwarded to
			// the consumer goroutine because the timer is not safe to reset
			// from this one.
			select {
			case payloads <- payload{keepAlive: true}:
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
			if item.keepAlive {
				continue
			}
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
func ClassifiedError(statusCode int, message string, secrets ...string) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// Lead with an actionable instruction rather than the raw upstream auth blurb
		// (which often points the user at the wrong provider's dashboard URL). Keep a
		// redacted, one-line upstream detail for context — never the raw body. (AUDIT-H7)
		curated := "auth error: your API key is missing or invalid — run `zero auth`, or set the provider's API key, then retry."
		if detail := strings.TrimSpace(Redact(message, secrets...)); detail != "" {
			return curated + " (provider said: " + detail + ")"
		}
		return curated
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, 529:
		return Redact("rate limit error: "+message, secrets...)
	default:
		prefix := "provider error: "
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			prefix = "provider request error: "
		}
		return Redact(prefix+message, secrets...)
	}
}

// tokenShape matches a long credential-like token (API key / JWT) so the Bearer
// heuristic in Redact only scrubs an actual token, not ordinary words.
var tokenShape = regexp.MustCompile(`^[A-Za-z0-9._\-]{16,}$`)

// looksLikeToken reports whether w is credential-shaped: long, token-charset, and
// either very long or containing a digit (so "Bearer authentication" / "Bearer
// token" in upstream help text is not mangled, while real keys/JWTs are redacted).
func looksLikeToken(w string) bool {
	w = strings.Trim(w, ".,;:\"'`)(")
	if !tokenShape.MatchString(w) {
		return false
	}
	if len(w) >= 24 {
		return true
	}
	return strings.ContainsAny(w, "0123456789")
}

// Redact removes known API-key and bearer-token forms from provider messages.
func Redact(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	words := strings.Fields(message)
	for index := 0; index < len(words)-1; index++ {
		// Only redact the word after "Bearer" when it is actually token-shaped, so the
		// provider's own help text ("use Bearer authentication", "Bearer token") is no
		// longer corrupted into "authorization [REDACTED]". (AUDIT-H7)
		if strings.EqualFold(strings.TrimRight(words[index], ":"), "Bearer") && looksLikeToken(words[index+1]) {
			words[index+1] = "[REDACTED]"
		}
	}
	return strings.Join(words, " ")
}

// upstreamFailureMarkers are transport-level failures that mean a request never
// reached the model: the server the client connected to could not establish a
// connection to its upstream. They distinguish a connectivity problem (outside
// the agent's control) from the model rejecting the request.
var upstreamFailureMarkers = []string{
	"TLS handshake timeout",
	"context deadline exceeded",
	"connection refused",
	"no such host",
	"network is unreachable",
	"i/o timeout",
}

// UpstreamUnreachable detects a provider error that is really a connectivity
// failure to an upstream host rather than a model/request error, and rewrites it
// into a clear, actionable message. The common case is a local Ollama daemon
// serving a "-cloud" model: it answers on localhost but returns HTTP 502 because
// it cannot reach its own cloud backend, surfacing an opaque proxied string like
// `Post "https://ollama.com:443/...": net/http: TLS handshake timeout`. It
// matches only when both a transport failure marker and a concrete host are
// present, so the agent's own request-deadline cancellations are left untouched.
// Non-matching messages are returned unchanged with false.
func UpstreamUnreachable(message string) (string, bool) {
	reason := ""
	for _, marker := range upstreamFailureMarkers {
		if strings.Contains(message, marker) {
			reason = marker
			break
		}
	}
	host := upstreamHost(message)
	if reason == "" || host == "" {
		return message, false
	}

	return "upstream unreachable: the model server could not connect to " + host +
		" (" + reason + "). The request never reached the model — this is a network failure " +
		"between the model server and its upstream, not a model error. Verify the host is " +
		"reachable from the machine running the model server (DNS/proxy/VPN/firewall); a local " +
		"daemon proxying a cloud model — e.g. an Ollama daemon serving a \"-cloud\" model — must " +
		"itself be able to reach the internet.", true
}

// upstreamHost extracts the unreachable host from a Go transport error. It
// handles the two shapes these errors take: a quoted request URL
// (`... "https://host:port/path": ...`) and a raw dial target
// (`dial tcp host:port: ...`). The URL form is preferred when both are present.
// It returns "" when neither yields a host.
func upstreamHost(message string) string {
	if index := strings.Index(message, "\"http"); index >= 0 {
		rest := message[index+1:]
		if end := strings.IndexByte(rest, '"'); end >= 0 {
			if parsed, err := url.Parse(rest[:end]); err == nil && parsed.Host != "" {
				return parsed.Host
			}
		}
	}
	const dialPrefix = "dial tcp "
	if index := strings.Index(message, dialPrefix); index >= 0 {
		rest := message[index+len(dialPrefix):]
		if end := strings.Index(rest, ": "); end >= 0 {
			rest = rest[:end]
		}
		if host := strings.TrimSpace(rest); host != "" && !strings.Contains(host, " ") {
			return host
		}
	}
	return ""
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
