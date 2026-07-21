package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxMCPRedirects = 10

// mcpDialTimeout bounds establishing the TCP connection to an MCP server.
const mcpDialTimeout = 10 * time.Second

// mcpDialKeepAlive matches net/http's DefaultTransport dialer so idle
// connection health checks behave the same as the rest of the process.
const mcpDialKeepAlive = 30 * time.Second

// mcpTLSHandshakeTimeout bounds completing the TLS handshake once connected.
const mcpTLSHandshakeTimeout = 10 * time.Second

// mcpResponseHeaderTimeout bounds waiting for response headers after the
// request has been written. It does not bound reading the response body, so
// a long-running or streamed tool call is not cut off once the server starts
// responding.
const mcpResponseHeaderTimeout = 30 * time.Second

// mcpTransport is the default RoundTripper for MCP HTTP clients. It clones
// http.DefaultTransport -- preserving proxy-from-environment, HTTP/2
// negotiation, and idle connection pooling -- and only tightens the
// connection-establishment timeouts and adds a response-header timeout.
// Connection setup is bounded here instead of via http.Client.Timeout, so a
// slow or unreachable server fails fast without capping the total lifetime
// of a legitimate long-running or streamed tool call.
var mcpTransport http.RoundTripper = newMCPTransport()

func newMCPTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   mcpDialTimeout,
		KeepAlive: mcpDialKeepAlive,
	}).DialContext
	transport.TLSHandshakeTimeout = mcpTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = mcpResponseHeaderTimeout
	return transport
}

func mcpHTTPClient(server Server, transport http.RoundTripper) *http.Client {
	if transport == nil {
		transport = mcpTransport
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: checkMCPRedirect(server),
	}
}

func checkMCPRedirect(server Server) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if len(via) >= maxMCPRedirects {
			return fmt.Errorf("MCP %s server %s stopped after %d redirects", server.Type, server.Name, maxMCPRedirects)
		}
		if len(via) == 0 {
			return nil
		}
		if !sameMCPOrigin(via[0].URL, request.URL) {
			return fmt.Errorf("MCP %s server %s refused cross-origin redirect to %s", server.Type, server.Name, mcpOrigin(request.URL))
		}
		return nil
	}
}

func sameMCPOrigin(left *url.URL, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectiveMCPPort(left) == effectiveMCPPort(right)
}

func effectiveMCPPort(value *url.URL) string {
	if value == nil {
		return ""
	}
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func mcpOrigin(value *url.URL) string {
	if value == nil {
		return "<nil>"
	}
	host := value.Hostname()
	port := effectiveMCPPort(value)
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	if value.Scheme == "" {
		return host
	}
	return strings.ToLower(value.Scheme) + "://" + host
}

// routeAndDo performs the HTTP request using streaming vs finite semantics based on req.
// If execTimeout is nil, defaults (30s for finite, unbounded for streaming) apply.
func routeAndDo(ctx context.Context, req *http.Request, execTimeout *time.Duration) (*http.Response, error) {
	return DoToolHTTPRequest(ctx, req, execTimeout)
}
