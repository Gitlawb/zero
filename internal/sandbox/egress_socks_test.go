package sandbox

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

// socks5Connect performs a SOCKS5 no-auth greeting and a CONNECT request for the
// given ATYP/address/port through proxyAddr, returning the open connection and
// the server's REP byte. The caller closes the connection.
func socks5Connect(t *testing.T, proxyAddr string, atyp byte, addr []byte, port int) (net.Conn, byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial socks proxy: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Greeting: VER=5, NMETHODS=1, METHOD=0 (no auth).
	if _, err := conn.Write([]byte{socksVersion5, 0x01, socksNoAuth}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if method[0] != socksVersion5 || method[1] != socksNoAuth {
		t.Fatalf("method selection = %v, want [5 0]", method)
	}

	// Request: VER=5, CMD=CONNECT, RSV=0, ATYP, ADDR, PORT(2, big-endian).
	request := append([]byte{socksVersion5, socksCmdConnect, 0x00, atyp}, addr...)
	request = append(request, byte(port>>8), byte(port&0xff))
	if _, err := conn.Write(request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	reply := make([]byte, 10) // VER, REP, RSV, ATYP(IPv4), BND.ADDR(4), BND.PORT(2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return conn, reply[1]
}

func TestEgressProxySocksAddrBindsLoopback(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"github.com"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	if proxy.SocksAddr() == "" {
		t.Fatal("SocksAddr must be non-empty while the proxy is running")
	}
	if proxy.SocksAddr() == proxy.Addr() {
		t.Fatalf("SOCKS and HTTP listeners must be distinct ports, both %q", proxy.Addr())
	}
	host, _, err := net.SplitHostPort(proxy.SocksAddr())
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", proxy.SocksAddr(), err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("SOCKS bound to %q, want loopback 127.0.0.1", host)
	}
}

// TestEgressProxySocksRejectsUnsupportedAuth verifies the proxy honors SOCKS5
// method negotiation: a client offering only methods the proxy does not support
// (here user/pass 0x02, no no-auth) gets the "no acceptable methods" reply (0xFF)
// rather than the proxy forcing a method the client never advertised.
func TestEgressProxySocksRejectsUnsupportedAuth(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"github.com"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	conn, err := net.DialTimeout("tcp", proxy.SocksAddr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial socks proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Greeting: VER=5, NMETHODS=1, METHOD=0x02 (user/pass) — no-auth NOT offered.
	if _, err := conn.Write([]byte{socksVersion5, 0x01, 0x02}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if resp[0] != socksVersion5 || resp[1] != socksNoAcceptable {
		t.Fatalf("method selection = %v, want [5 0xFF] (no acceptable methods)", resp)
	}
}

// TestEgressProxySocksConnectAllowed verifies an allowlisted SOCKS5 CONNECT is
// authorized through the SAME gate, dialed, and relayed to a fake upstream.
func TestEgressProxySocksConnectAllowed(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstream.Close()
	go func() {
		c, aerr := upstream.Accept()
		if aerr != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4)
		if _, rerr := io.ReadFull(c, buf); rerr != nil {
			return
		}
		_, _ = c.Write([]byte("pong"))
	}()

	_, portStr, _ := net.SplitHostPort(upstream.Addr().String())
	port, _ := strconv.Atoi(portStr)

	// Allow the loopback IP so the IPv4 CONNECT is authorized.
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	conn, rep := socks5Connect(t, proxy.SocksAddr(), socksAtypIPv4, net.IPv4(127, 0, 0, 1).To4(), port)
	defer conn.Close()
	if rep != socksRepSuccess {
		t.Fatalf("SOCKS CONNECT to allowed host REP = %#x, want success (0)", rep)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write through tunnel: %v", err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read through tunnel: %v", err)
	}
	if string(got) != "pong" {
		t.Fatalf("tunneled response = %q, want pong", string(got))
	}
}

// TestEgressProxySocksConnectDenied verifies a non-allowlisted SOCKS5 CONNECT
// (domain form) is refused with the "connection not allowed" reply.
func TestEgressProxySocksConnectDenied(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{Allowed: []string{"allowed.example"}})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	domain := "denied.example"
	addr := append([]byte{byte(len(domain))}, []byte(domain)...)
	conn, rep := socks5Connect(t, proxy.SocksAddr(), socksAtypDomain, addr, 443)
	conn.Close()
	if rep != socksRepNotAllowed {
		t.Fatalf("SOCKS CONNECT to denied host REP = %#x, want not-allowed (2)", rep)
	}
}

// TestEgressProxySocksDenyWinsOverAllow verifies the SOCKS gate honors
// deny-wins: a denied subdomain of an allowed domain is refused (REP 2), proving
// the SOCKS path runs the same authorize()/domainAllowed() logic as the HTTP
// proxy rather than a parallel, weaker check.
func TestEgressProxySocksDenyWinsOverAllow(t *testing.T) {
	proxy, err := newEgressProxy(egressOptions{
		Allowed: []string{"allowed.example"},
		Denied:  []string{"blocked.allowed.example"},
	})
	if err != nil {
		t.Fatalf("newEgressProxy: %v", err)
	}
	defer proxy.Close()

	denied := "blocked.allowed.example"
	addr := append([]byte{byte(len(denied))}, []byte(denied)...)
	conn, rep := socks5Connect(t, proxy.SocksAddr(), socksAtypDomain, addr, 443)
	conn.Close()
	if rep != socksRepNotAllowed {
		t.Fatalf("denied subdomain REP = %#x, want not-allowed (2) (deny must win)", rep)
	}
}
