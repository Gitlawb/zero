package mcp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ToolCallType classifies HTTP calls for timeout semantics.
type ToolCallType int

const (
	ToolCallStreaming ToolCallType = iota
	ToolCallFinite
)

// HTTPClientConfig centralizes transport-related timeouts.
type HTTPClientConfig struct {
	DialTimeout         time.Duration
	TLSHandshakeTimeout time.Duration
	StreamIdleTimeout   time.Duration
	DefaultExecTimeout  time.Duration
}

// HTTPClients holds separate clients for streaming vs bounded calls.
type HTTPClients struct {
	Streaming *http.Client
	Bounded   *http.Client
	cfg       HTTPClientConfig
}

// NewHTTPClients builds Streaming and Bounded http.Clients with shared dialer and HTTP/2.
func NewHTTPClients(cfg HTTPClientConfig) *HTTPClients {
	baseDialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: 30 * time.Second,
	}

	// Unbounded response header timeout for streaming; rely on idle watchdog.
	streamingTransport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           baseDialer.DialContext,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		DisableCompression:    false,
		ResponseHeaderTimeout: 0,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		// ForceAttemptHTTP2 restores HTTP/2 support using net/http's own
		// vendored implementation. A custom DialContext/TLSClientConfig
		// conservatively disables automatic HTTP/2 negotiation unless this
		// is set (see http.Transport's doc comment); it requires no
		// external module, unlike golang.org/x/net/http2.ConfigureTransport
		// (which this package previously depended on without ever adding
		// golang.org/x/net to go.mod/go.sum -- a guaranteed
		// "no required module provides package" build failure).
		ForceAttemptHTTP2: true,
	}

	// Bounded requests rely on per-request context deadlines.
	boundedTransport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           baseDialer.DialContext,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		DisableCompression:    false,
		ResponseHeaderTimeout: 0,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
	}

	return &HTTPClients{
		Streaming: &http.Client{Transport: streamingTransport},
		Bounded:   &http.Client{Transport: boundedTransport},
		cfg:       cfg,
	}
}

// NewHTTPClientsFromClients wraps already-configured streaming and bounded
// http.Clients so ToolHTTP's classify/timeout/idle-watchdog behavior can be
// reused by callers that need to preserve additional per-server transport
// semantics beyond NewHTTPClients' bare defaults -- e.g. the MCP
// networkClient's OAuth bearer-token round tripper and cross-origin redirect
// guard (see mcpHTTPClient/oauthHTTPClient in network_client.go and
// network_redirect.go). Only cfg.StreamIdleTimeout and cfg.DefaultExecTimeout
// are consulted for clients built this way; DialTimeout/TLSHandshakeTimeout
// are ignored since the given clients' transports are already fully formed.
func NewHTTPClientsFromClients(streaming, bounded *http.Client, cfg HTTPClientConfig) *HTTPClients {
	return &HTTPClients{Streaming: streaming, Bounded: bounded, cfg: cfg}
}

// ToolHTTP routes requests to the appropriate client and enforces deadlines.
type ToolHTTP struct {
	clients *HTTPClients
}

// NewToolHTTP creates a ToolHTTP from clients.
func NewToolHTTP(clients *HTTPClients) *ToolHTTP {
	return &ToolHTTP{clients: clients}
}

// Do executes the request according to call type. For ToolCallFinite, an execTimeout
// overrides the default if non-nil and >0. Streaming calls may use an idle watchdog.
func (c *ToolHTTP) Do(ctx context.Context, req *http.Request, kind ToolCallType, execTimeout *time.Duration) (*http.Response, error) {
	switch kind {
	case ToolCallFinite:
		t := c.clients.cfg.DefaultExecTimeout
		if execTimeout != nil && *execTimeout > 0 {
			t = *execTimeout
		}
		ctx2, cancel := context.WithTimeout(ctx, t)
		req = req.WithContext(ctx2)
		resp, err := c.clients.Bounded.Do(req)
		if err != nil {
			cancel()
			return nil, err
		}
		// Release ctx2 when the caller closes the body, not as soon as Do()
		// returns. A bare `defer cancel()` at this call's own scope would
		// fire the instant Do() returns -- i.e. right after headers arrive,
		// before the caller has read the body -- cancelling ctx2 and
		// aborting any real (non-empty) response body with
		// context.Canceled. The context.WithTimeout deadline still fires on
		// its own if reading the body takes longer than t.
		resp.Body = &cancelOnCloseReadCloser{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	case ToolCallStreaming:
		// Unbounded execution; apply optional idle watchdog.
		if c.clients.cfg.StreamIdleTimeout > 0 {
			ctx2, cancel := context.WithCancel(ctx)
			req = req.WithContext(ctx2)
			resp, err := c.clients.Streaming.Do(req)
			if err != nil {
				cancel()
				return nil, err
			}
			resp.Body = newIdleWatchdogReadCloser(resp.Body, c.clients.cfg.StreamIdleTimeout, cancel)
			return resp, nil
		}
		req = req.WithContext(ctx)
		return c.clients.Streaming.Do(req)
	default:
		return nil, fmt.Errorf("unknown ToolCallType: %d", kind)
	}
}

// cancelOnCloseReadCloser defers releasing a ToolCallFinite request's
// context.WithTimeout cancel func until the response body is closed. See the
// comment at its construction site in ToolHTTP.Do for why this must not
// happen any earlier.
type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnCloseReadCloser) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}

// idleWatchdogReader cancels context if no bytes are read for timeout.
type idleWatchdogReader struct {
	r       io.ReadCloser
	timer   *time.Timer
	timeout time.Duration
	cancel  context.CancelFunc
	mu      sync.Mutex
}

func newIdleWatchdogReadCloser(r io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) io.ReadCloser {
	iw := &idleWatchdogReader{
		r:       r,
		timeout: timeout,
		cancel:  cancel,
	}
	iw.timer = time.AfterFunc(timeout, iw.fire)
	return iw
}

func (iw *idleWatchdogReader) fire() {
	// Cancel the request context on idle timeout.
	iw.cancel()
}

func (iw *idleWatchdogReader) Read(p []byte) (int, error) {
	n, err := iw.r.Read(p)
	iw.mu.Lock()
	if iw.timer != nil && n > 0 {
		iw.timer.Reset(iw.timeout)
	}
	iw.mu.Unlock()
	return n, err
}

func (iw *idleWatchdogReader) Close() error {
	iw.mu.Lock()
	if iw.timer != nil {
		iw.timer.Stop()
		iw.timer = nil
	}
	iw.mu.Unlock()
	return iw.r.Close()
}

// NewDefaultToolHTTP builds ToolHTTP with confirmed defaults.
func NewDefaultToolHTTP() *ToolHTTP {
	cfg := HTTPClientConfig{
		DialTimeout:         5 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		StreamIdleTimeout:   10 * time.Minute,
		DefaultExecTimeout:  30 * time.Second,
	}
	return NewToolHTTP(NewHTTPClients(cfg))
}

// ClassifyToolCall determines whether a request should be treated as streaming.
// Heuristics: SSE (Accept: text/event-stream), WebSocket upgrade, or explicit stream query.
//
// Note: this header-based heuristic cannot distinguish an MCP tools/call
// request from initialize/tools/list/notifications, because the MCP
// Streamable HTTP spec has the client send the same
// `Accept: application/json, text/event-stream` header on every POST
// regardless of JSON-RPC method. MCP's networkClient therefore classifies by
// method name instead (see toolCallTypeFor in network_client.go) and calls
// ToolHTTP.Do directly rather than routing through this function. It remains
// the right classifier for generic (non-MCP-method-aware) callers -- see
// routeAndDo in network_redirect.go.
func ClassifyToolCall(req *http.Request) ToolCallType {
	accept := req.Header.Get("Accept")
	if strings.Contains(strings.ToLower(accept), "text/event-stream") {
		return ToolCallStreaming
	}
	if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return ToolCallStreaming
	}
	if strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade") {
		return ToolCallStreaming
	}
	if req.URL != nil && strings.EqualFold(req.URL.Query().Get("stream"), "1") {
		return ToolCallStreaming
	}
	// Default: finite (e.g., JSON POST)
	return ToolCallFinite
}

// DoToolHTTPRequest is a convenience wrapper using NewDefaultToolHTTP.
// execTimeout applies only to finite calls; pass nil to use default.
func DoToolHTTPRequest(ctx context.Context, req *http.Request, execTimeout *time.Duration) (*http.Response, error) {
	return NewDefaultToolHTTP().Do(ctx, req, ClassifyToolCall(req), execTimeout)
}
