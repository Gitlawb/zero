package providerhealth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func TestDialValidatedAddrsFallsBackPastDeadAddress(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("203.0.113.1"), // dead first record
		netip.MustParseAddr("203.0.113.2"), // reachable second record
	}
	var dialed []string
	dial := func(_ context.Context, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		if address == "203.0.113.2:443" {
			return stubConn{}, nil
		}
		return nil, errors.New("connection refused")
	}

	conn, err := dialValidatedAddrs(context.Background(), addrs, "443", dial)
	if err != nil {
		t.Fatalf("expected fallback to the reachable address, got err %v", err)
	}
	_ = conn.Close()
	if len(dialed) != 2 || dialed[0] != "203.0.113.1:443" || dialed[1] != "203.0.113.2:443" {
		t.Fatalf("expected both addresses dialed in order, got %v", dialed)
	}
}

func TestDialValidatedAddrsReturnsLastErrorWhenAllFail(t *testing.T) {
	addrs := []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("203.0.113.2")}
	dial := func(_ context.Context, _ string) (net.Conn, error) {
		return nil, errors.New("unreachable")
	}
	if _, err := dialValidatedAddrs(context.Background(), addrs, "443", dial); err == nil {
		t.Fatal("expected an error when every address fails to dial")
	}
}

type stubConn struct{ net.Conn }

func (stubConn) Close() error { return nil }

func TestSafeDialContextRejectsResolvedPrivateAddress(t *testing.T) {
	dial := safeDialContext(staticResolver{addr: netip.MustParseAddr("10.0.0.5")})

	conn, err := dial(context.Background(), "tcp", "api.example.com:443")
	if conn != nil {
		_ = conn.Close()
		t.Fatal("dialed a host that resolved to a private address")
	}
	var safety endpointSafetyError
	if !errors.As(err, &safety) {
		t.Fatalf("err = %v, want endpointSafetyError", err)
	}
}

func TestSafeDialContextRejectsLiteralLinkLocalAddress(t *testing.T) {
	// The cloud metadata address must be refused even when supplied as a literal,
	// without consulting the resolver and without opening a socket.
	dial := safeDialContext(staticResolver{err: errors.New("resolver must not be called")})

	conn, err := dial(context.Background(), "tcp", "169.254.169.254:80")
	if conn != nil {
		_ = conn.Close()
		t.Fatal("dialed a link-local literal address")
	}
	var safety endpointSafetyError
	if !errors.As(err, &safety) {
		t.Fatalf("err = %v, want endpointSafetyError", err)
	}
}

func TestConnectivityClientRefusesRedirectToBlockedHost(t *testing.T) {
	client := newConnectivityClient(5*time.Second, staticResolver{addr: netip.MustParseAddr("169.254.169.254")})
	if client.CheckRedirect == nil {
		t.Fatal("default connectivity client has no CheckRedirect guard")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://metadata.internal/latest", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("CheckRedirect allowed a redirect to a metadata address")
	}
}

func TestConnectivityClientRefusesTooManyRedirects(t *testing.T) {
	client := newConnectivityClient(5*time.Second, staticResolver{addr: netip.MustParseAddr("93.184.216.34")})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/v1/models", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	via := make([]*http.Request, maxConnectivityRedirects)
	if err := client.CheckRedirect(req, via); err == nil {
		t.Fatalf("CheckRedirect allowed more than %d redirects", maxConnectivityRedirects)
	}
}
