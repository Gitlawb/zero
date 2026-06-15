package sandbox

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// MITM TLS interception (opt-in via Policy.InspectTLS). Mirrors
// reference-sandbox-wsl-mitm-code-agent-js/sandbox-manager.js mitmCA/getMitmCA
// and network/proxy.js. The proxy terminates TLS toward the SANDBOXED CLIENT
// using an ephemeral, locally generated CA so it can read the decrypted request
// and apply the SAME allow/deny gate on the real Host — visibility, not new
// authority. The UPSTREAM connection is always validated against real roots
// (never InsecureSkipVerify).

// mitmCA is an ephemeral certificate authority that mints per-host leaf certs.
// It lives only for the proxy's lifetime; only its PUBLIC cert is ever exposed
// (so the sandboxed client can trust the minted leaves).
type mitmCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certDER []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// maxMITMLeafCacheEntries caps the per-host leaf cache so an attacker cannot grow
// it without bound. Combined with minting for the AUTHORIZED host (not arbitrary
// client SNI), this keeps the cache size to roughly the allowlist size.
const maxMITMLeafCacheEntries = 512

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("mitm: serial: %w", err)
	}
	return serial, nil
}

// newMITMCA generates a fresh ECDSA CA. The key never leaves the process.
func newMITMCA() (*mitmCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mitm: generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "zero sandbox MITM CA", Organization: []string{"zero-sandbox"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("mitm: create CA cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("mitm: parse CA cert: %w", err)
	}
	return &mitmCA{cert: cert, key: key, certDER: der, leaves: map[string]*tls.Certificate{}}, nil
}

// caPEM returns the CA's PUBLIC certificate in PEM (safe to write/expose).
func (ca *mitmCA) caPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.certDER})
}

// leafFor mints (and caches) a leaf certificate for host, signed by the CA, with
// the CA appended so the chain verifies against the CA alone.
func (ca *mitmCA) leafFor(host string) (*tls.Certificate, error) {
	host = hostnameOnly(host)
	if host == "" {
		return nil, fmt.Errorf("mitm: empty host for leaf cert")
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if leaf, ok := ca.leaves[host]; ok {
		return leaf, nil
	}
	// Bound the cache: past the cap, mint without caching so memory stays bounded
	// (a small per-request cost rather than unbounded growth).
	cacheable := len(ca.leaves) < maxMITMLeafCacheEntries
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &leafKey.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("mitm: mint leaf for %s: %w", host, err)
	}
	leaf := &tls.Certificate{Certificate: [][]byte{der, ca.certDER}, PrivateKey: leafKey}
	if cacheable {
		ca.leaves[host] = leaf
	}
	return leaf, nil
}

// writeMITMCAFile writes the CA's PUBLIC cert PEM to an owner-only temp file and
// returns its path, so the runner can surface it via ZERO_SANDBOX_CA_CERT.
func writeMITMCAFile(pemBytes []byte) (string, error) {
	f, err := os.CreateTemp("", "zero-sandbox-ca-*.pem")
	if err != nil {
		return "", fmt.Errorf("mitm: create CA file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if _, err := f.Write(pemBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// handleConnectMITM terminates TLS toward the client (minting a leaf for the
// requested host), then for each decrypted request re-checks the real Host
// through the SAME authorize gate and forwards it upstream with full system-root
// validation. A denied host gets a 403 over the decrypted channel and is never
// forwarded.
func (proxy *egressProxy) handleConnectMITM(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if target == "" {
		target = r.URL.Host
	}
	if !proxy.authorizeTarget(target, 443) {
		proxy.logDecision("deny", "CONNECT", target)
		http.Error(w, "scoped egress: host not allowed", http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "scoped egress: hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()
	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}

	host := hostnameOnly(target)
	tlsConn := tls.Server(clientConn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// Mint for the AUTHORIZED CONNECT host, never the arbitrary client SNI:
			// honoring SNI would let an attacker churn distinct names to force
			// unbounded key generation and cache growth.
			return proxy.mitm.leafFor(host)
		},
	})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer tlsConn.Close()

	reader := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return // client closed or EOF
		}
		reqHost := req.Host
		if reqHost == "" {
			reqHost = host
		}
		// MITM widens VISIBILITY, never authority: the decrypted Host passes the
		// SAME full authorize() gate as the CONNECT check (deny-wins, empty allowlist
		// => deny, and an interactive DomainPrompt is honored consistently).
		if !proxy.authorizeTarget(reqHost, 443) {
			proxy.logDecision("deny", req.Method, reqHost)
			writeMITMResponse(tlsConn, http.StatusForbidden, "scoped egress: host not allowed")
			return
		}
		req.URL.Scheme = "https"
		req.URL.Host = normalizeConnectTarget(target)
		req.RequestURI = ""
		resp, err := proxy.mitmTransport.RoundTrip(req)
		if err != nil {
			proxy.logDecision("deny", req.Method, reqHost)
			writeMITMResponse(tlsConn, http.StatusBadGateway, "scoped egress: upstream request failed")
			return
		}
		proxy.logDecision("allow", req.Method, reqHost)
		// Re-frame the response as HTTP/1.1 for the decrypted (HTTP/1.1) client
		// connection, regardless of how the upstream replied.
		resp.Proto, resp.ProtoMajor, resp.ProtoMinor = "HTTP/1.1", 1, 1
		writeErr := resp.Write(tlsConn)
		_ = resp.Body.Close()
		if writeErr != nil || req.Close || resp.Close {
			return
		}
	}
}

// writeMITMResponse writes a minimal plaintext HTTP/1.1 response over the
// decrypted client connection.
func writeMITMResponse(conn net.Conn, code int, msg string) {
	body := msg + "\n"
	resp := &http.Response{
		StatusCode:    code,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	_ = resp.Write(conn)
}
