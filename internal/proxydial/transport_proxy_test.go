package proxydial

import (
	"net/http"
	"testing"
)

// The dialer asks the transport at dial time, so it follows a Proxy that was
// set after the dialer was installed, and a transport with no Proxy exempts
// nothing.
func TestTransportProxyFollowsTheTransportAtCallTime(t *testing.T) {
	transport := &http.Transport{}
	proxyFor := TransportProxy(transport)
	if IsProxyTarget(proxyFor, "127.0.0.1:3067") {
		t.Fatal("a transport with no Proxy exempted an address")
	}
	transport.Proxy = fixedProxy("http://127.0.0.1:3067")
	if !IsProxyTarget(proxyFor, "127.0.0.1:3067") {
		t.Fatal("the dialer did not follow the Proxy set on the transport after it was installed")
	}
	if IsProxyTarget(TransportProxy(nil), "127.0.0.1:3067") {
		t.Fatal("a nil transport exempted an address")
	}
}
