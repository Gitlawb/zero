package sandbox

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEgressProxyPassthroughNoCAByDefault(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"github.com"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()
	if proxy.mitm != nil {
		t.Fatal("InspectTLS off must not create a MITM CA (pure passthrough)")
	}
	if proxy.CACertPEM() != nil {
		t.Fatal("passthrough proxy must expose no CA cert")
	}
}

func TestMITMCAMintsChainingLeaf(t *testing.T) {
	ca, err := newMITMCA()
	if err != nil {
		t.Fatalf("newMITMCA: %v", err)
	}
	leaf, err := ca.leafFor("api.example.com")
	if err != nil {
		t.Fatalf("leafFor: %v", err)
	}
	leafCert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.caPEM()) {
		t.Fatal("append CA PEM")
	}
	if _, err := leafCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "api.example.com",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("minted leaf must chain to the CA: %v", err)
	}
	// Per-host caching.
	if leaf2, _ := ca.leafFor("api.example.com"); leaf2 != leaf {
		t.Fatal("leafFor must cache one leaf per host")
	}
}

// mitmConnect performs a CONNECT through the proxy then opens a TLS client that
// trusts ONLY the MITM CA, returning the decrypted client connection.
func mitmConnect(t *testing.T, proxyAddr, target string, caPEM []byte, serverName string) *tls.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	// Bound the whole exchange (CONNECT read + TLS handshake + request/response) so
	// a regression cannot hang the test indefinitely.
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}
	if br.Buffered() != 0 {
		t.Fatalf("proxy over-sent %d bytes after CONNECT 200", br.Buffered())
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append MITM CA PEM")
	}
	tlsConn := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: serverName})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("client TLS handshake trusting the MITM CA: %v", err)
	}
	return tlsConn
}

func TestEgressProxyMITMForwardsAllowlistedHost(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "upstream-ok")
	}))
	defer upstream.Close()
	host := mustHost(t, upstream.URL)
	target := mustHostPort(t, upstream.URL)

	// Trust the upstream's real cert explicitly (NOT InsecureSkipVerify): proves
	// the MITM still validates the upstream chain.
	upstreamRoots := x509.NewCertPool()
	upstreamRoots.AddCert(upstream.Certificate())

	proxy, err := newEgressProxy(egressOptions{Allowed: []string{host}, InspectTLS: true, upstreamRoots: upstreamRoots})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()
	if proxy.CACertPEM() == nil {
		t.Fatal("InspectTLS must generate a CA")
	}

	tlsConn := mitmConnect(t, proxy.Addr(), target, proxy.CACertPEM(), host)
	defer tlsConn.Close()
	fmt.Fprintf(tlsConn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read decrypted response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("allowlisted forwarded request status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read decrypted body: %v", err)
	}
	if !strings.Contains(string(body), "upstream-ok") {
		t.Fatalf("MITM did not forward the allowlisted request, got %q", string(body))
	}
}

func TestEgressProxyMITMBlocksDeniedDecryptedHost(t *testing.T) {
	// CONNECT to an allowed host, but send a Host header that is NOT allowlisted —
	// the MITM re-checks the DECRYPTED Host and blocks it before forwarding.
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"127.0.0.1"}, InspectTLS: true})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	tlsConn := mitmConnect(t, proxy.Addr(), "127.0.0.1:443", proxy.CACertPEM(), "127.0.0.1")
	defer tlsConn.Close()
	fmt.Fprint(tlsConn, "GET / HTTP/1.1\r\nHost: blocked.example\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatalf("read decrypted response: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied decrypted Host status = %d, want 403", resp.StatusCode)
	}
}
