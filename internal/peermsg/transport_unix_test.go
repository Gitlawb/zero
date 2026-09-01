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

func TestUnixTransportCanonicalizesSymlinkedRoot(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "zp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove temporary transport root: %v", err)
		}
	})
	target := filepath.Join(root, "physical")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	endpoint, err := (unixTransport{}).Endpoint(alias, "0123456789abcdef", 4242)
	if err != nil {
		t.Fatal(err)
	}
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(endpoint, canonicalTarget+string(filepath.Separator)) {
		t.Fatalf("endpoint %q is not rooted at canonical target %q", endpoint, canonicalTarget)
	}
}

func TestUnixTransportRejectsPathLongerThanFallback(t *testing.T) {
	transport := unixTransport{}
	longTmp := filepath.Join(t.TempDir(), strings.Repeat("x", unixSocketPathMax))
	t.Setenv("TMPDIR", longTmp)
	if _, err := transport.Endpoint(filepath.Join(t.TempDir(), strings.Repeat("y", unixSocketPathMax)), "0123456789abcdef", 4242); err == nil {
		t.Fatal("expected too-long fallback path error")
	}
}
