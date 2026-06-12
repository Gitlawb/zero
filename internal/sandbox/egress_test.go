package sandbox

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDomainAllowedMatchingSemantics(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		allowed []string
		denied  []string
		want    bool
	}{
		{name: "exact match", host: "github.com", allowed: []string{"github.com"}, want: true},
		{name: "subdomain match", host: "api.github.com", allowed: []string{"github.com"}, want: true},
		{name: "deep subdomain match", host: "a.b.github.com", allowed: []string{"github.com"}, want: true},
		{name: "host with port", host: "github.com:443", allowed: []string{"github.com"}, want: true},
		{name: "subdomain with port", host: "api.github.com:443", allowed: []string{"github.com"}, want: true},
		{name: "case insensitive", host: "API.GitHub.COM", allowed: []string{"github.com"}, want: true},
		{name: "trailing dot host", host: "github.com.", allowed: []string{"github.com"}, want: true},

		{name: "no false suffix prefix", host: "notgithub.com", allowed: []string{"github.com"}, want: false},
		{name: "no false suffix superstring label", host: "evilgithub.com", allowed: []string{"github.com"}, want: false},
		{name: "no false suffix appended domain", host: "github.com.evil.com", allowed: []string{"github.com"}, want: false},
		{name: "unrelated host", host: "example.org", allowed: []string{"github.com"}, want: false},
		{name: "empty allowlist denies", host: "github.com", allowed: nil, want: false},

		{name: "deny overrides allow exact", host: "github.com", allowed: []string{"github.com"}, denied: []string{"github.com"}, want: false},
		{name: "deny overrides allow subdomain", host: "api.github.com", allowed: []string{"github.com"}, denied: []string{"github.com"}, want: false},
		{name: "deny specific subdomain only", host: "secret.github.com", allowed: []string{"github.com"}, denied: []string{"secret.github.com"}, want: false},
		{name: "allow sibling when deny is specific", host: "api.github.com", allowed: []string{"github.com"}, denied: []string{"secret.github.com"}, want: true},

		{name: "multiple allow entries", host: "registry.npmjs.org", allowed: []string{"github.com", "npmjs.org"}, want: true},
		{name: "empty host denied", host: "", allowed: []string{"github.com"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := domainAllowed(tc.host, tc.allowed, tc.denied)
			if got != tc.want {
				t.Fatalf("domainAllowed(%q, %v, %v) = %v, want %v", tc.host, tc.allowed, tc.denied, got, tc.want)
			}
		})
	}
}

func TestNewEgressProxyEmptyAllowlistFailsClosed(t *testing.T) {
	if _, err := newEgressProxy(egressOptions{Allowed: nil}); err == nil {
		t.Fatal("newEgressProxy with empty allowlist = nil error, want fail-closed error")
	}
	if _, err := newEgressProxy(egressOptions{Allowed: []string{"   "}}); err == nil {
		t.Fatal("newEgressProxy with blank-only allowlist = nil error, want fail-closed error")
	}
}

func TestEgressProxyBindsLoopback(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"github.com"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()
	host, _, err := net.SplitHostPort(proxy.Addr())
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", proxy.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("proxy bound to %q, want loopback 127.0.0.1", host)
	}
}

// TestEgressProxyHTTPAuthorizesURLHostNotHostHeader verifies an absolute-URL
// request to a DENIED host carrying an ALLOWED Host header is refused —
// authorization follows the dialed URL host, not the spoofable Host header.
func TestEgressProxyHTTPAuthorizesURLHostNotHostHeader(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"allowed.example"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	req := httptest.NewRequest(http.MethodGet, "http://denied.example/data", nil)
	req.Host = "allowed.example" // spoofed allowed Host header
	rec := httptest.NewRecorder()
	proxy.handleHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (Host header must not authorize a different URL host)", rec.Code)
	}
}

// TestEgressProxyConnectAllowed verifies an allowlisted CONNECT tunnels through
// to a fake upstream TLS server.
func TestEgressProxyConnectAllowed(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream-ok")
	}))
	defer upstream.Close()
	upstreamHost := mustHost(t, upstream.URL)

	// Allow the upstream host so the tunnel is permitted.
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{upstreamHost}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	conn, err := net.DialTimeout("tcp", proxy.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	target := mustHostPort(t, upstream.URL)
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}

	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("tls handshake through tunnel: %v", err)
	}
	fmt.Fprintf(tlsConn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target)
	body, err := io.ReadAll(tlsConn)
	if err != nil {
		t.Fatalf("read tunneled response: %v", err)
	}
	if !strings.Contains(string(body), "upstream-ok") {
		t.Fatalf("tunneled body = %q, want upstream-ok", string(body))
	}
}

// TestEgressProxyConnectRefused verifies a non-allowlisted CONNECT is refused
// with 403 and no tunnel is established.
func TestEgressProxyConnectRefused(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"github.com"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	conn, err := net.DialTimeout("tcp", proxy.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	fmt.Fprint(conn, "CONNECT notgithub.com:443 HTTP/1.1\r\nHost: notgithub.com:443\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("CONNECT status = %d, want 403 for denied host", resp.StatusCode)
	}
}

// TestEgressProxyHTTPRefused verifies a plain-HTTP request to a denied host is
// blocked with 403.
func TestEgressProxyHTTPRefused(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"github.com"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustURL(t, "http://"+proxy.Addr())),
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://denied.example.com/path")
	if err != nil {
		t.Fatalf("proxied GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("HTTP status = %d, want 403 for denied host", resp.StatusCode)
	}
}

// TestEgressProxyHTTPAllowed verifies a plain-HTTP request to an allowlisted
// host is forwarded to the upstream.
func TestEgressProxyHTTPAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "http-upstream-ok")
	}))
	defer upstream.Close()
	upstreamHost := mustHost(t, upstream.URL)

	proxy, err := newEgressProxy(egressOptions{Allowed: []string{upstreamHost}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustURL(t, "http://"+proxy.Addr())),
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get(upstream.URL + "/path")
	if err != nil {
		t.Fatalf("proxied GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "http-upstream-ok") {
		t.Fatalf("allowed HTTP status=%d body=%q, want 200 http-upstream-ok", resp.StatusCode, string(body))
	}
}

// TestEgressProxyRedactsDeniedURLLogs verifies a denied URL containing a
// ?token=... query string is not logged raw (the token is redacted).
func TestEgressProxyRedactsDeniedURLLogs(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	proxy, err := newEgressProxy(egressOptions{
		Allowed: []string{"github.com"},
		Log: func(line string) {
			mu.Lock()
			logs = append(logs, line)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	const secret = "super-secret-token-value-1234567890"
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustURL(t, "http://"+proxy.Addr())),
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://denied.example.com/path?token=" + secret)
	if err != nil {
		t.Fatalf("proxied GET: %v", err)
	}
	resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 {
		t.Fatal("expected a deny log line, got none")
	}
	joined := strings.Join(logs, "\n")
	if strings.Contains(joined, secret) {
		t.Fatalf("deny log leaked the raw token:\n%s", joined)
	}
	// Sanity: the deny decision was logged for the denied host.
	if !strings.Contains(joined, "denied.example.com") {
		t.Fatalf("deny log missing host:\n%s", joined)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return parsed
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	host, _, err := net.SplitHostPort(mustHostPort(t, raw))
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	return host
}

func mustHostPort(t *testing.T, raw string) string {
	t.Helper()
	parsed := mustURL(t, raw)
	return parsed.Host
}
