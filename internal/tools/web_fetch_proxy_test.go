package tools

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

// THE PROXY IS NOT THE TARGET. With HTTPS_PROXY pointing at a local forward
// proxy, the transport dials the proxy and tunnels the request through it. The
// dialer used to refuse that dial as a loopback address, so every fetch on a
// machine behind such a proxy failed with "loopback hosts are blocked" for a
// public target that had already been validated (#569). The dial to the
// proxy's own address skips the pin, and this test also pins that nothing
// beside that address does.
func TestWebFetchSafeDialLetsTheConfiguredProxyThroughAndNothingElse(t *testing.T) {
	proxyURL, err := url.Parse("http://127.0.0.1:3067")
	if err != nil {
		t.Fatal(err)
	}
	proxyFor := func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	var dialedAddress string
	stop := errors.New("stop after address capture")
	dial := webFetchSafeDialContext(
		// The proxy address is a literal the transport chose from configuration
		// the user set directly; it is dialed as given, not resolved and pinned.
		webFetchResolverFunc(func(_ context.Context, network string, host string) ([]netip.Addr, error) {
			t.Fatalf("resolver consulted for the proxy dial: network=%q host=%q", network, host)
			return nil, nil
		}),
		webFetchDialFunc(func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialedAddress = address
			return nil, stop
		}),
		proxyFor,
	)

	_, err = dial(context.Background(), "tcp", "127.0.0.1:3067")
	if !errors.Is(err, stop) {
		t.Fatalf("the dial to the configured proxy was refused: %v", err)
	}
	if dialedAddress != "127.0.0.1:3067" {
		t.Fatalf("dialed address = %q, want the proxy address as given", dialedAddress)
	}

	// AND ONLY THAT ADDRESS. A sibling loopback port is not the proxy, and it
	// is refused before the dialer runs.
	dialedAddress = ""
	_, err = dial(context.Background(), "tcp", "127.0.0.1:3068")
	if err == nil || !strings.Contains(err.Error(), "loopback hosts are blocked") {
		t.Fatalf("sibling loopback port: err = %v, want loopback rejection", err)
	}
	if dialedAddress != "" {
		t.Fatalf("a loopback port other than the proxy's was dialed: %s", dialedAddress)
	}
}

// Without a proxy the guard is exactly what it was: loopback is refused and
// the dialer never runs.
func TestWebFetchSafeDialStillRefusesLoopbackWithoutAProxy(t *testing.T) {
	dialCalled := false
	dial := webFetchSafeDialContext(
		webFetchResolverFunc(func(_ context.Context, network string, host string) ([]netip.Addr, error) {
			t.Fatalf("resolver consulted for a loopback literal: network=%q host=%q", network, host)
			return nil, nil
		}),
		webFetchDialFunc(func(context.Context, string, string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("dial should not run")
		}),
		nil,
	)

	_, err := dial(context.Background(), "tcp", "127.0.0.1:3067")
	if err == nil || !strings.Contains(err.Error(), "loopback hosts are blocked") {
		t.Fatalf("expected loopback rejection, got %v", err)
	}
	if dialCalled {
		t.Fatal("loopback was dialed with no proxy configured")
	}
}
