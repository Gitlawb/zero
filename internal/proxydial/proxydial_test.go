package proxydial

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func fixedProxy(raw string) ProxyFunc {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return func(*http.Request) (*url.URL, error) { return parsed, nil }
}

// THE EXEMPTION IS ONE ENDPOINT, NOT A HOST.
func TestIsProxyTargetMatchesExactlyTheProxyEndpoint(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		proxy   string
		address string
		want    bool
	}{
		{"the configured loopback proxy", "http://127.0.0.1:3067", "127.0.0.1:3067", true},
		{"same host, another port", "http://127.0.0.1:3067", "127.0.0.1:6379", false},
		{"same port, another loopback host", "http://127.0.0.1:3067", "127.0.0.2:3067", false},
		{"a public target that is not the proxy", "http://127.0.0.1:3067", "93.184.216.34:443", false},
		{"hostname proxy, case-insensitive", "http://LocalHost:3067", "localhost:3067", true},
		{"ipv6 proxy, bracket forms agree", "http://[::1]:3067", "[::1]:3067", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsProxyTarget(fixedProxy(testCase.proxy), testCase.address); got != testCase.want {
				t.Errorf("IsProxyTarget(%s, %s) = %v, want %v", testCase.proxy, testCase.address, got, testCase.want)
			}
		})
	}
}

// A MISSING PROXY PORT IS THE SCHEME DEFAULT, NOT A WILDCARD.
//
// http://127.0.0.1 makes the transport dial 127.0.0.1:80. Treating the missing
// port as "any port" would exempt 127.0.0.1:6379 on a direct dial, which NO_PROXY
// can produce for the same host, and that is a loopback bypass dressed as a
// convenience.
func TestIsProxyTargetTakesAMissingPortAsTheSchemeDefault(t *testing.T) {
	if !IsProxyTarget(fixedProxy("http://127.0.0.1"), "127.0.0.1:80") {
		t.Error("an http proxy with no port did not match the port the transport dials, 80")
	}
	if IsProxyTarget(fixedProxy("http://127.0.0.1"), "127.0.0.1:6379") {
		t.Error("an http proxy with no port matched an unrelated loopback port, which is the SSRF bypass the exact match exists to close")
	}
	if !IsProxyTarget(fixedProxy("https://127.0.0.1"), "127.0.0.1:443") {
		t.Error("an https proxy with no port did not match 443")
	}
	if IsProxyTarget(fixedProxy("https://127.0.0.1"), "127.0.0.1:80") {
		t.Error("an https proxy with no port matched 80, which is not the port the transport dials for it")
	}
}

// No proxy, no exemption: the guard is exactly what it was.
func TestIsProxyTargetIsFalseWithoutAProxy(t *testing.T) {
	if IsProxyTarget(nil, "127.0.0.1:3067") {
		t.Error("a nil proxy function produced an exemption")
	}
	none := func(*http.Request) (*url.URL, error) { return nil, nil }
	if IsProxyTarget(none, "127.0.0.1:3067") {
		t.Error("a proxy function that returns no proxy produced an exemption")
	}
	failing := func(*http.Request) (*url.URL, error) { return nil, errors.New("bad proxy config") }
	if IsProxyTarget(failing, "127.0.0.1:3067") {
		t.Error("a failing proxy function produced an exemption")
	}
}

// A scheme-dependent proxy choice is honoured for either scheme, since the
// dialer does not know which scheme the request that reached it used.
func TestIsProxyTargetAsksForBothSchemes(t *testing.T) {
	httpsOnly := func(request *http.Request) (*url.URL, error) {
		if request.URL.Scheme == "https" {
			return url.Parse("http://127.0.0.1:3067")
		}
		return nil, nil
	}
	if !IsProxyTarget(httpsOnly, "127.0.0.1:3067") {
		t.Error("a proxy configured only for https was not recognised")
	}
	if IsProxyTarget(httpsOnly, "127.0.0.1:8080") {
		t.Error("an unrelated port matched")
	}
}

// An address the dialer could never have been handed is not matched.
func TestIsProxyTargetRejectsAnAddressWithoutAPort(t *testing.T) {
	if IsProxyTarget(fixedProxy("http://127.0.0.1:3067"), "127.0.0.1") {
		t.Error("a bare host with no port matched")
	}
}
