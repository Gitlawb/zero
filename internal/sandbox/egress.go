package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
)

// egressProxy is a local, in-process filtering HTTP proxy used to give a
// sandboxed process *scoped* network egress: it may reach an explicit set of
// domains and nothing else. It handles both HTTP CONNECT (the HTTPS tunnel
// method) and plain HTTP forward-proxy requests. Every connection's target
// host is checked against the allow/deny lists; a denied target is refused
// (403 / failed CONNECT) and never tunneled.
//
// The proxy binds to an ephemeral loopback port (127.0.0.1:0) so only the local
// machine can reach it; the chosen address is exported into the sandbox env as
// HTTP_PROXY/HTTPS_PROXY/ALL_PROXY by the runner.
//
// Safety: the proxy fails closed. An empty effective allowlist is rejected at
// construction; an unparseable target host is denied; any forwarding error
// closes the connection rather than opening unrestricted access.
type egressProxy struct {
	listener net.Listener
	server   *http.Server
	// socksListener serves the SOCKS5 CONNECT front-end on a SEPARATE loopback
	// port. It runs the same authorize()/domainAllowed() gate as the HTTP proxy, so
	// a client configured with ALL_PROXY=socks5://<socksAddr> is filtered
	// identically. Bound to 127.0.0.1:0 like the HTTP listener.
	socksListener net.Listener
	allowed       []string
	denied        []string
	log           func(string)

	// domainPrompt, when set, is asked to authorize an UNKNOWN host (one neither
	// allowed nor explicitly denied) instead of failing closed. It must return
	// within promptTimeout — the passed context is cancelled on timeout so a
	// well-behaved callback can abort instead of leaking — or the request is denied.
	// Decisions are cached for the proxy's lifetime so a host is prompted at most once.
	domainPrompt  func(ctx context.Context, host string, port int) (bool, error)
	promptTimeout time.Duration

	cacheMu       sync.Mutex
	decisionCache map[string]bool
	// inflight tracks an in-progress prompt per host:port so concurrent requests
	// for the same unknown target wait for the first prompt instead of each firing
	// their own (a prompt storm) and racing to write the final cached decision.
	inflight map[string]chan struct{}

	closeOnce sync.Once
}

// egressOptions configures a scoped-egress proxy. Allowed lists the domains the
// sandboxed process may reach (exact host or any subdomain). Denied is
// subtracted from Allowed and always wins. Log, when set, receives one line per
// allow/deny decision; lines are passed through the repo redaction so query
// tokens are never written to the audit trail.
type egressOptions struct {
	Allowed []string
	Denied  []string
	Log     func(string)
	// DomainPrompt, when set, authorizes an unknown host at request time (e.g. by
	// prompting the user) instead of failing closed. PromptTimeout bounds the wait
	// (default 60s); a timeout or error denies, and the context passed to the
	// callback is cancelled on timeout so it can abort rather than leak a goroutine.
	// Explicitly denied hosts are never prompted. Leave nil to keep the strict
	// fail-closed behavior.
	DomainPrompt  func(ctx context.Context, host string, port int) (bool, error)
	PromptTimeout time.Duration
}

// errEmptyAllowlist is returned when a scoped-egress proxy is requested with no
// allowlisted domains. Scoped egress with an empty allowlist must behave like a
// full network deny, so the caller treats this as "deny", never "allow".
var errEmptyAllowlist = errors.New("scoped egress requires at least one allowed domain")

func newEgressProxy(options egressOptions) (*egressProxy, error) {
	allowed := normalizeDomains(options.Allowed)
	if len(allowed) == 0 {
		return nil, errEmptyAllowlist
	}
	denied := normalizeDomains(options.Denied)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind scoped-egress proxy: %w", err)
	}
	// Bind the SOCKS5 front-end on its own loopback port. Fail closed: if it
	// cannot bind, tear down the HTTP listener and return an error so the caller
	// treats scoped egress as a full deny rather than starting a partial proxy.
	socksListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("bind scoped-egress socks proxy: %w", err)
	}

	proxy := &egressProxy{
		listener:      listener,
		socksListener: socksListener,
		allowed:       allowed,
		denied:        denied,
		log:           options.Log,
		domainPrompt:  options.DomainPrompt,
		promptTimeout: options.PromptTimeout,
		decisionCache: map[string]bool{},
		inflight:      map[string]chan struct{}{},
	}
	proxy.server = &http.Server{
		Handler:           http.HandlerFunc(proxy.handle),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		// Serve returns ErrServerClosed on Close; nothing else to do.
		_ = proxy.server.Serve(listener)
	}()
	go proxy.serveSocks(socksListener)
	return proxy, nil
}

// Addr returns the loopback host:port the HTTP proxy is listening on.
func (proxy *egressProxy) Addr() string {
	if proxy == nil || proxy.listener == nil {
		return ""
	}
	return proxy.listener.Addr().String()
}

// SocksAddr returns the loopback host:port the SOCKS5 front-end is listening on,
// or "" when no SOCKS listener is active.
func (proxy *egressProxy) SocksAddr() string {
	if proxy == nil || proxy.socksListener == nil {
		return ""
	}
	return proxy.socksListener.Addr().String()
}

// Close stops the proxy and releases its listener. It is safe to call multiple
// times.
func (proxy *egressProxy) Close() error {
	if proxy == nil {
		return nil
	}
	var err error
	proxy.closeOnce.Do(func() {
		if proxy.server != nil {
			err = proxy.server.Close()
		}
		// Closing the SOCKS listener unblocks serveSocks's Accept loop so the
		// goroutine returns. Report its error only if the HTTP close succeeded.
		if proxy.socksListener != nil {
			if cerr := proxy.socksListener.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	})
	return err
}

func (proxy *egressProxy) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		proxy.handleConnect(w, r)
		return
	}
	proxy.handleHTTP(w, r)
}

// handleConnect implements the HTTPS tunnel: it authorizes the CONNECT target,
// then (if allowed) hijacks the client connection and blindly relays bytes
// between the client and the upstream. The proxy never sees plaintext TLS; the
// allow/deny decision is made purely on the requested host.
func (proxy *egressProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if target == "" {
		target = r.URL.Host
	}
	if !proxy.authorizeTarget(target, 443) {
		proxy.logDecision("deny", "CONNECT", target)
		http.Error(w, "scoped egress: host not allowed", http.StatusForbidden)
		return
	}

	upstream, err := net.DialTimeout("tcp", normalizeConnectTarget(target), 30*time.Second)
	if err != nil {
		proxy.logDecision("deny", "CONNECT", target)
		http.Error(w, "scoped egress: upstream dial failed", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "scoped egress: hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "scoped egress: hijack failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	proxy.logDecision("allow", "CONNECT", target)
	relay(clientConn, upstream)
}

// handleHTTP implements the plain-HTTP forward proxy: it authorizes the target
// host, then re-issues the request upstream and copies the response back. A
// denied host gets a 403 and the request is never forwarded.
func (proxy *egressProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Authorize the host actually dialed. A forward-proxy request carries an
	// absolute URL whose host is the upstream target, so authorize r.URL.Host
	// and fall back to the Host header only when the request line had no host.
	// Trusting the Host header here would let `GET http://denied/ Host: allowed`
	// bypass the allowlist while the transport still dials the URL host.
	target := r.URL.Host
	if target == "" {
		target = r.Host
	}
	if !proxy.authorizeTarget(target, 80) {
		proxy.logDecision("deny", r.Method, requestURLString(r))
		http.Error(w, "scoped egress: host not allowed", http.StatusForbidden)
		return
	}

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	// A forward-proxy request carries an absolute URL; ensure the scheme/host
	// are populated so the transport can dial the upstream.
	if outbound.URL.Scheme == "" {
		outbound.URL.Scheme = "http"
	}
	if outbound.URL.Host == "" {
		outbound.URL.Host = target
	}

	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	resp, err := transport.RoundTrip(outbound)
	if err != nil {
		proxy.logDecision("deny", r.Method, requestURLString(r))
		http.Error(w, "scoped egress: upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	proxy.logDecision("allow", r.Method, requestURLString(r))
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// logDecision records a single allow/deny decision through the repo redaction so
// a target carrying a query token (e.g. ?token=...) is never written raw.
func (proxy *egressProxy) logDecision(decision string, method string, target string) {
	if proxy == nil || proxy.log == nil {
		return
	}
	safe := redaction.RedactString(target, redaction.Options{})
	proxy.log(fmt.Sprintf("scoped-egress %s %s %s", decision, method, safe))
}

// authorize decides whether a connection to host:port may proceed. An allowed
// host passes; an explicitly denied host is refused without prompting; an unknown
// host fails closed unless a domain-prompt callback authorizes it (cached for the
// proxy's lifetime, bounded by promptTimeout).
func (proxy *egressProxy) authorize(host string, port int) bool {
	if domainAllowed(host, proxy.allowed, proxy.denied) {
		return true
	}
	for _, entry := range proxy.denied {
		if domainMatches(host, entry) {
			return false // explicit deny never prompts
		}
	}
	if proxy.domainPrompt == nil {
		return false // fail closed: no way to authorize an unknown host
	}
	return proxy.promptForHost(host, port)
}

func (proxy *egressProxy) promptForHost(host string, port int) bool {
	// Key the decision cache by host:port, not host alone — the prompt authorizes a
	// specific (host, port), so caching by host would let one "allow" silently
	// authorize every other port on that host for the proxy's lifetime.
	key := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(host)), port)
	for {
		proxy.cacheMu.Lock()
		if decided, ok := proxy.decisionCache[key]; ok {
			proxy.cacheMu.Unlock()
			return decided
		}
		if wait, ok := proxy.inflight[key]; ok {
			// Another goroutine is already prompting for this exact target; wait for
			// it to finish, then re-read its cached decision instead of prompting
			// again, so a burst of connections triggers a single prompt.
			proxy.cacheMu.Unlock()
			<-wait
			continue
		}
		// We own the prompt for this key. Register an in-flight marker others wait on.
		wait := make(chan struct{})
		if proxy.inflight == nil {
			proxy.inflight = map[string]chan struct{}{}
		}
		proxy.inflight[key] = wait
		proxy.cacheMu.Unlock()

		allow := proxy.runDomainPrompt(host, port)

		proxy.cacheMu.Lock()
		proxy.decisionCache[key] = allow
		delete(proxy.inflight, key)
		close(wait)
		proxy.cacheMu.Unlock()
		return allow
	}
}

// runDomainPrompt invokes the callback with a timeout. A timeout or error denies.
// The context is cancelled when the wait ends (timeout or answer received), giving
// a cooperative callback a chance to abort instead of leaking its goroutine.
func (proxy *egressProxy) runDomainPrompt(host string, port int) bool {
	timeout := proxy.promptTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		allow, err := proxy.domainPrompt(ctx, host, port)
		done <- allow && err == nil
	}()
	select {
	case allow := <-done:
		return allow
	case <-ctx.Done():
		return false // timeout (ctx is cancelled, signalling the callback to abort)
	}
}

// authorizeTarget splits a host[:port] target and authorizes it; defaultPort is
// used when the target carries no port.
func (proxy *egressProxy) authorizeTarget(target string, defaultPort int) bool {
	host := hostnameOnly(target)
	if host == "" {
		return false
	}
	port := defaultPort
	if _, portStr, err := net.SplitHostPort(target); err == nil {
		// Parse the numeric port directly rather than net.LookupPort, which can
		// consult the (context-unaware) system resolver for named ports.
		if parsed, perr := strconv.Atoi(portStr); perr == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
	}
	return proxy.authorize(host, port)
}

// relay copies bytes in both directions between two connections until either
// side closes, then returns. It is the byte-pump for an established CONNECT
// tunnel.
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Unblock the paired copy by signalling EOF/closure on the other half.
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		} else {
			_ = dst.SetReadDeadline(time.Now())
		}
	}
	go copyHalf(a, b)
	go copyHalf(b, a)
	wg.Wait()
}

// domainAllowed reports whether host may be reached under the given allow/deny
// lists. Matching semantics (host is lowercased and stripped of any :port and
// trailing dot first):
//
//   - An entry matches host when host equals the entry OR host is a subdomain of
//     the entry (host ends with "."+entry). So "github.com" matches "github.com"
//     and "api.github.com", but NOT "notgithub.com" (no label boundary) nor
//     "github.com.evil.com" (entry is not a suffix at a label boundary).
//   - host is allowed only if it matches at least one Allowed entry AND matches
//     no Denied entry. Deny always wins (deny overrides allow), and the deny
//     check uses the same exact-or-subdomain rule, so denying "github.com" blocks
//     every github.com subdomain while denying "secret.github.com" blocks only
//     that subtree.
//   - An empty host, or an empty allowlist, is denied (fail closed).
func domainAllowed(host string, allowed []string, denied []string) bool {
	normalized := hostnameOnly(host)
	if normalized == "" {
		return false
	}
	matched := false
	for _, entry := range allowed {
		if domainMatches(normalized, entry) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	for _, entry := range denied {
		if domainMatches(normalized, entry) {
			return false
		}
	}
	return true
}

// domainMatches reports whether host is exactly entry or a subdomain of entry.
// Both arguments must already be lowercased host-only forms.
func domainMatches(host string, entry string) bool {
	if entry == "" || host == "" {
		return false
	}
	if host == entry {
		return true
	}
	return strings.HasSuffix(host, "."+entry)
}

// hostnameOnly lowercases host, strips any :port and a single trailing dot, and
// returns the bare hostname. It returns "" for input that has no usable host.
func hostnameOnly(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	// Strip a :port when present; SplitHostPort fails on a bare host, in which
	// case the original value is the host.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

// normalizeConnectTarget ensures a CONNECT target carries an explicit port,
// defaulting to 443 (the HTTPS port CONNECT is used for) when one is absent.
func normalizeConnectTarget(target string) string {
	if _, _, err := net.SplitHostPort(target); err == nil {
		return target
	}
	return net.JoinHostPort(strings.TrimSuffix(target, "."), "443")
}

// normalizeDomains lowercases, trims, host-normalizes, and de-duplicates a list
// of domain entries, dropping blanks.
func normalizeDomains(domains []string) []string {
	if len(domains) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(domains))
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		normalized := hostnameOnly(domain)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

// requestURLString returns a printable form of the request target for logging.
// It is always passed through redaction before being written.
func requestURLString(r *http.Request) string {
	if r.URL != nil && r.URL.String() != "" {
		return r.URL.String()
	}
	return r.Host
}
