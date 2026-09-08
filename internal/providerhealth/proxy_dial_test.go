package providerhealth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// THE PROXY IS NOT THE TARGET, AND THE GUARD HAS TO KNOW THE DIFFERENCE.
//
// With HTTPS_PROXY set to a local forward proxy, the transport dials the proxy
// first and tunnels the request through it. This dialer refused that dial as a
// loopback address, so `zero doctor --connectivity` failed with "proxyconnect
// tcp: ... loopback hosts are blocked" for a target that had already been
// validated and was public (#569). The exemption is the proxy's own address and
// nothing beside it, which the second half of this test pins.
func TestSafeDialContextLetsTheConfiguredProxyThroughAndNothingElse(t *testing.T) {
	// A live listener stands in for the local proxy, so the exempted dial is a
	// real connect and not a stub that would pass for any address.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	proxyAddr := listener.Addr().String()
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	proxyFor := func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	// The resolver must not be consulted for the proxy dial: the address is a
	// literal the transport chose, and resolving it would be a second opinion
	// on configuration the user set directly.
	dial := safeDialContext(staticResolver{err: errors.New("resolver must not be called")}, false, proxyFor)

	conn, err := dial(context.Background(), "tcp", proxyAddr)
	if err != nil {
		t.Fatalf("the dial to the configured proxy was refused: %v", err)
	}
	_ = conn.Close()

	// AND ONLY THAT ADDRESS. A sibling loopback port is still a loopback dial
	// and is refused before any socket is opened.
	_, port, _ := net.SplitHostPort(proxyAddr)
	number, _ := strconv.Atoi(port)
	sibling := net.JoinHostPort("127.0.0.1", strconv.Itoa(number+1))
	conn, err = dial(context.Background(), "tcp", sibling)
	if conn != nil {
		_ = conn.Close()
		t.Fatalf("a loopback port other than the proxy's was dialed: %s", sibling)
	}
	var safety endpointSafetyError
	if !errors.As(err, &safety) {
		t.Fatalf("sibling loopback port: err = %v, want endpointSafetyError", err)
	}
}

// Without a proxy the guard is exactly what it was: loopback is refused.
func TestSafeDialContextStillRefusesLoopbackWithoutAProxy(t *testing.T) {
	dial := safeDialContext(staticResolver{err: errors.New("resolver must not be called")}, false, nil)
	conn, err := dial(context.Background(), "tcp", "127.0.0.1:3067")
	if conn != nil {
		_ = conn.Close()
		t.Fatal("loopback was dialed with no proxy configured")
	}
	var safety endpointSafetyError
	if !errors.As(err, &safety) {
		t.Fatalf("err = %v, want endpointSafetyError", err)
	}
}

// THE WIRING, END TO END. The client's dialer asks the client's own transport
// for its proxy, so a request whose transport routes through a loopback proxy
// reaches that proxy. The proxy here is a live listener that records the first
// line it receives and hangs up; the request fails after that, and the point
// is that the CONNECT arrived rather than dying in the dialer as "loopback
// hosts are blocked". The proxy is set on the transport directly rather than
// through HTTPS_PROXY because http.ProxyFromEnvironment reads the environment
// once per process, which would make this test depend on test order.
func TestConnectivityClientTunnelsThroughTheConfiguredProxy(t *testing.T) {
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

	client := newConnectivityClient(3*time.Second, staticResolver{err: errors.New("resolver must not be called")}, nil, false)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	transport.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(request)
	select {
	case first := <-arrived:
		if line := strings.SplitN(first, "\r\n", 2)[0]; line != "CONNECT api.example.com:443 HTTP/1.1" {
			t.Fatalf("proxy received %q, want a CONNECT for the validated target", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the proxy never received a connection; request error: %v", err)
	}
}

// With nothing substituted the transport keeps a Proxy function, which is
// what makes HTTPS_PROXY take effect at all.
func TestConnectivityClientKeepsAProxyFunction(t *testing.T) {
	client := newConnectivityClient(0, staticResolver{}, nil, false)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("the connectivity transport has no Proxy function, so HTTPS_PROXY is ignored and a proxied probe fails as loopback")
	}
}
