package providerhealth

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

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
