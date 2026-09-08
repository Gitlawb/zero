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
	"time"
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

// THE WIRING, END TO END. The transport's dialer asks that same transport for
// its proxy, so a fetch routed through a loopback proxy reaches the proxy. The
// proxy is a live listener that records the first line it receives and hangs
// up; the request fails after that, and the point is that the CONNECT arrived
// rather than dying in the dialer as "loopback hosts are blocked". The proxy
// is set on the transport directly rather than through HTTPS_PROXY because
// http.ProxyFromEnvironment reads the environment once per process, which
// would make this test depend on test order.
func TestWebFetchTransportTunnelsThroughTheConfiguredProxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	arrived := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		n, _ := conn.Read(buf)
		arrived <- string(buf[:n])
	}()
	proxyURL, err := url.Parse("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	// A proxied fetch never resolves the target on this side; the CONNECT
	// carries the hostname and the proxy resolves it.
	roundTripper := webFetchSafeTransport(nil, webFetchResolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return nil, errors.New("resolver must not be called for a proxied fetch")
	}))
	transport, ok := roundTripper.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", roundTripper)
	}
	transport.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}

	_, err = client.Get("https://public.example/resource")
	select {
	case first := <-arrived:
		if line := strings.SplitN(first, "\r\n", 2)[0]; line != "CONNECT public.example:443 HTTP/1.1" {
			t.Fatalf("proxy received %q, want a CONNECT for the target", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the proxy never received a connection; request error: %v", err)
	}
}
