//go:build unix

package peermsg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixTransportFallbackAndSocketMode(t *testing.T) {
	transport := unixTransport{}
	longRoot := filepath.Join(t.TempDir(), strings.Repeat("long", 30))
	endpoint, err := transport.Endpoint(longRoot, "0123456789abcdef", 4242)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoint) > unixSocketPathMax {
		t.Fatalf("endpoint length = %d", len(endpoint))
	}
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close(); _ = transport.Remove(endpoint) })
	info, err := os.Stat(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o", info.Mode().Perm())
	}
}

func TestUnixTransportRejectsPathLongerThanFallback(t *testing.T) {
	transport := unixTransport{}
	oldTmp := os.Getenv("TMPDIR")
	longTmp := filepath.Join(t.TempDir(), strings.Repeat("x", unixSocketPathMax))
	if err := os.Setenv("TMPDIR", longTmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("TMPDIR", oldTmp) })
	if _, err := transport.Endpoint(filepath.Join(t.TempDir(), strings.Repeat("y", unixSocketPathMax)), "0123456789abcdef", 4242); err == nil {
		t.Fatal("expected too-long fallback path error")
	}
}
