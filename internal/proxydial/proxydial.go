// Package proxydial answers one question for an SSRF-guarded dialer: is the
// address it has been asked to dial the transport's own forward proxy?
//
// A guarded transport validates the TARGET of a request before dialing, and its
// dialer refuses loopback, private and link-local addresses so a redirect or a
// DNS rebind cannot walk the request onto the local network. When a forward
// proxy is configured, the transport dials the PROXY first and tunnels the
// request through it, and a local proxy is by far the common shape:
// HTTPS_PROXY=http://127.0.0.1:3067. The dialer then sees 127.0.0.1, refuses it,
// and the user gets "loopback hosts are blocked" for a request whose real target
// was validated and public (#569).
//
// The proxy address is not the request target, and the guard was never about it.
// It is configuration the user set on purpose, and the transport is going to dial
// it for every request whether the guard likes it or not. So a dial to exactly
// that address is let through, and only that address.
package proxydial

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ProxyFunc is the shape of http.Transport.Proxy.
type ProxyFunc func(*http.Request) (*url.URL, error)

// TransportProxy is a ProxyFunc that asks transport for its Proxy at call
// time rather than copying the field at construction. A dialer installed on
// the transport then exempts exactly the proxy that transport dials, and a
// test can substitute transport.Proxy after the client is built and drive a
// real request through the whole chain without touching the environment.
func TransportProxy(transport *http.Transport) ProxyFunc {
	return func(request *http.Request) (*url.URL, error) {
		if transport == nil || transport.Proxy == nil {
			return nil, nil
		}
		return transport.Proxy(request)
	}
}

// IsProxyTarget reports whether address is the host:port that proxyFor would
// have the transport dial for an https or http request.
//
// proxyFor should be TransportProxy of the transport the dialer is installed
// on, not a fresh read of the environment. http.ProxyFromEnvironment caches
// the environment once per process, so a separate read can disagree with what
// the transport actually uses, and it also makes the check untestable without
// re-executing the binary. Asking the transport itself means the exemption
// matches the dial it is exempting, by construction.
//
// THE PORT IS MATCHED EXACTLY, with a missing proxy port taken as the scheme
// default the transport would dial. Treating a missing port as "any port" would
// turn HTTPS_PROXY=http://127.0.0.1 into an exemption for every loopback port on
// a direct dial, which NO_PROXY can produce for the same host. A proxy URL names
// one endpoint; the exemption is that endpoint and nothing beside it.
func IsProxyTarget(proxyFor ProxyFunc, address string) bool {
	if proxyFor == nil {
		return false
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	for _, scheme := range []string{"https", "http"} {
		// The probe host is a placeholder under a reserved TLD: the transport's
		// proxy choice can depend on the request scheme, so both are asked, but
		// it must not depend on a host that could collide with a real one.
		probe := &http.Request{URL: &url.URL{Scheme: scheme, Host: "proxy-target.invalid"}}
		proxyURL, err := proxyFor(probe)
		if err != nil || proxyURL == nil {
			continue
		}
		if !strings.EqualFold(proxyURL.Hostname(), host) {
			continue
		}
		if proxyPort(proxyURL) == port {
			return true
		}
	}
	return false
}

// proxyPort is the port the transport dials for a proxy URL: the explicit one,
// else the default for the proxy's own scheme.
func proxyPort(proxyURL *url.URL) string {
	if port := proxyURL.Port(); port != "" {
		return port
	}
	if strings.EqualFold(proxyURL.Scheme, "https") {
		return "443"
	}
	return "80"
}
