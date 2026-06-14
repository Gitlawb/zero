package sandbox

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"time"
)

// SOCKS5 wire constants (RFC 1928). The proxy speaks only the no-auth CONNECT
// subset: enough for a sandboxed process configured with ALL_PROXY=socks5://...
// to tunnel TCP through the same allow/deny gate the HTTP proxy enforces.
const (
	socksVersion5          = 0x05
	socksNoAuth            = 0x00
	socksNoAcceptable      = 0xFF
	socksCmdConnect        = 0x01
	socksAtypIPv4          = 0x01
	socksAtypDomain        = 0x03
	socksAtypIPv6          = 0x04
	socksRepSuccess        = 0x00
	socksRepGeneralFailure = 0x01
	socksRepNotAllowed     = 0x02
	socksRepCmdUnsupported = 0x07
)

// socksHandshakeTimeout bounds the greeting+request exchange so a stalled or
// malformed client cannot pin a goroutine; it is cleared before the relay so an
// established tunnel is not torn down.
const socksHandshakeTimeout = 30 * time.Second

// serveSocks accepts SOCKS5 connections until the listener is closed.
func (proxy *egressProxy) serveSocks(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return // listener closed
		}
		go proxy.handleSocksConn(conn)
	}
}

// handleSocksConn runs one SOCKS5 CONNECT through the SAME authorize() gate as
// the HTTP proxy: an allowed (host, port) is dialed and relayed; a denied target
// gets a SOCKS "connection not allowed" reply and is never dialed. Fail-closed:
// any protocol/read error just closes the connection.
func (proxy *egressProxy) handleSocksConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(socksHandshakeTimeout))

	host, port, ok := socksNegotiate(conn)
	if !ok {
		return
	}
	targetLabel := net.JoinHostPort(host, strconv.Itoa(port))
	if !proxy.authorize(host, port) {
		proxy.logDecision("deny", "SOCKS", targetLabel)
		_ = socksReply(conn, socksRepNotAllowed)
		return
	}
	upstream, err := net.DialTimeout("tcp", targetLabel, 30*time.Second)
	if err != nil {
		proxy.logDecision("deny", "SOCKS", targetLabel)
		_ = socksReply(conn, socksRepGeneralFailure)
		return
	}
	defer upstream.Close()
	if err := socksReply(conn, socksRepSuccess); err != nil {
		return
	}
	// Clear the handshake deadline; the tunnel is long-lived.
	_ = conn.SetDeadline(time.Time{})
	proxy.logDecision("allow", "SOCKS", targetLabel)
	relay(conn, upstream)
}

// socksNegotiate performs the no-auth greeting and a CONNECT request, returning
// the requested host and port. ok=false means negotiation failed (an error reply
// has already been written where the protocol calls for one) and the caller
// should close the connection.
func socksNegotiate(conn net.Conn) (host string, port int, ok bool) {
	greeting := make([]byte, 2) // VER, NMETHODS
	if _, err := io.ReadFull(conn, greeting); err != nil || greeting[0] != socksVersion5 {
		return "", 0, false
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", 0, false
	}
	// RFC 1928: select a method the client actually offered. We only support
	// no-auth; if the client did not offer it, reply "no acceptable methods"
	// (0xFF) and abort rather than forcing a method it never advertised.
	supportsNoAuth := false
	for _, method := range methods {
		if method == socksNoAuth {
			supportsNoAuth = true
			break
		}
	}
	if !supportsNoAuth {
		_, _ = conn.Write([]byte{socksVersion5, socksNoAcceptable})
		return "", 0, false
	}
	if _, err := conn.Write([]byte{socksVersion5, socksNoAuth}); err != nil {
		return "", 0, false
	}

	header := make([]byte, 4) // VER, CMD, RSV, ATYP
	if _, err := io.ReadFull(conn, header); err != nil || header[0] != socksVersion5 {
		return "", 0, false
	}
	if header[1] != socksCmdConnect {
		_ = socksReply(conn, socksRepCmdUnsupported)
		return "", 0, false
	}
	switch header[3] {
	case socksAtypIPv4:
		addr := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, false
		}
		host = net.IP(addr).String()
	case socksAtypIPv6:
		addr := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, false
		}
		host = net.IP(addr).String()
	case socksAtypDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", 0, false
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return "", 0, false
		}
		host = string(name)
	default:
		_ = socksReply(conn, socksRepGeneralFailure)
		return "", 0, false
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return "", 0, false
	}
	return host, int(binary.BigEndian.Uint16(portBytes)), true
}

// socksReply writes a minimal SOCKS5 reply with the given status and a zero IPv4
// bound address (clients ignore BND.ADDR/BND.PORT for CONNECT).
func socksReply(conn net.Conn, status byte) error {
	_, err := conn.Write([]byte{socksVersion5, status, 0x00, socksAtypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
