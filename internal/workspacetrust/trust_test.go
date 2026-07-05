package workspacetrust

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// setUserConfigRoot redirects config.UserConfigDir() (via os.UserConfigDir) to a
// throwaway temp dir on every platform, so the trust store never touches the real
// user config directory. It mirrors internal/config/paths_test.go: os.UserConfigDir
// reads APPDATA on Windows, HOME on darwin, and XDG_CONFIG_HOME on Linux, so a single
// env var is not portable.
func setUserConfigRoot(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", root)
	case "darwin":
		t.Setenv("HOME", root)
	default:
		t.Setenv("XDG_CONFIG_HOME", root)
	}
}

func TestIsTrustedFreshStore(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	trusted, err := IsTrusted(dir)
	if err != nil {
		t.Fatalf("IsTrusted() error = %v, want nil", err)
	}
	if trusted {
		t.Fatalf("IsTrusted() = true, want false for a fresh store")
	}

	list, err := List()
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}
	if len(list) != 0 {
		t.Fatalf("List() = %v, want empty for a fresh store", list)
	}
}

func TestTrustThenIsTrusted(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	if err := Trust(dir); err != nil {
		t.Fatalf("Trust() error = %v", err)
	}

	trusted, err := IsTrusted(dir)
	if err != nil {
		t.Fatalf("IsTrusted() error = %v", err)
	}
	if !trusted {
		t.Fatalf("IsTrusted() = false, want true after Trust()")
	}
}

func TestIsTrustedExactMatchNoInheritance(t *testing.T) {
	setUserConfigRoot(t)
	repo := t.TempDir()
	if err := Trust(repo); err != nil {
		t.Fatalf("Trust() error = %v", err)
	}

	// Real subdirectories of the trusted repo must NOT be trusted.
	vendorEvil := filepath.Join(repo, "vendor", "evil")
	src := filepath.Join(repo, "src")
	if err := os.MkdirAll(vendorEvil, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", vendorEvil, err)
	}
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", src, err)
	}

	other := t.TempDir()

	for _, path := range []string{vendorEvil, src, other} {
		trusted, err := IsTrusted(path)
		if err != nil {
			t.Fatalf("IsTrusted(%q) error = %v", path, err)
		}
		if trusted {
			t.Fatalf("IsTrusted(%q) = true, want false (exact match only, no inheritance)", path)
		}
	}
}

func TestTrustIdempotent(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	if err := Trust(dir); err != nil {
		t.Fatalf("first Trust() error = %v", err)
	}
	if err := Trust(dir); err != nil {
		t.Fatalf("second Trust() error = %v", err)
	}

	list, err := List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List() = %v, want exactly one entry after two Trust() calls", list)
	}
}

func TestUntrust(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	if err := Trust(dir); err != nil {
		t.Fatalf("Trust() error = %v", err)
	}
	if err := Untrust(dir); err != nil {
		t.Fatalf("Untrust() error = %v", err)
	}

	trusted, err := IsTrusted(dir)
	if err != nil {
		t.Fatalf("IsTrusted() error = %v", err)
	}
	if trusted {
		t.Fatalf("IsTrusted() = true, want false after Untrust()")
	}
}

func TestUntrustAbsentIsNoOp(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	// Untrust on an absent path (empty store) must not error.
	if err := Untrust(dir); err != nil {
		t.Fatalf("Untrust() on absent path error = %v, want nil", err)
	}

	// And also when the store exists but does not contain the path.
	other := t.TempDir()
	if err := Trust(other); err != nil {
		t.Fatalf("Trust() error = %v", err)
	}
	if err := Untrust(dir); err != nil {
		t.Fatalf("Untrust() on absent-but-nonempty store error = %v, want nil", err)
	}
}

func TestNormalizationTrailingDot(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	if err := Trust(dir); err != nil {
		t.Fatalf("Trust() error = %v", err)
	}

	// Query with a non-canonical trailing "/." that resolves to the same target.
	query := filepath.Join(dir, ".")
	trusted, err := IsTrusted(query)
	if err != nil {
		t.Fatalf("IsTrusted(%q) error = %v", query, err)
	}
	if !trusted {
		t.Fatalf("IsTrusted(%q) = false, want true (non-canonical form of a trusted path)", query)
	}
}

func TestNormalizationSymlinkAlias(t *testing.T) {
	setUserConfigRoot(t)
	target := t.TempDir()
	if err := Trust(target); err != nil {
		t.Fatalf("Trust() error = %v", err)
	}

	// A symlink that resolves to the trusted target must match.
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("os.Symlink unsupported on this platform: %v", err)
	}

	trusted, err := IsTrusted(link)
	if err != nil {
		t.Fatalf("IsTrusted(%q) error = %v", link, err)
	}
	if !trusted {
		t.Fatalf("IsTrusted(%q) = false, want true (symlink alias of a trusted path)", link)
	}
}

func TestIsTrustedEmptyRoot(t *testing.T) {
	setUserConfigRoot(t)

	trusted, err := IsTrusted("")
	if err != nil {
		t.Fatalf("IsTrusted(\"\") error = %v, want nil", err)
	}
	if trusted {
		t.Fatalf("IsTrusted(\"\") = true, want false")
	}
}

func TestIsTrustedFailClosedOnReadError(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	// Create trust.json as a DIRECTORY so os.ReadFile fails with a non-ErrNotExist
	// error. This is portable (unlike chmod 0o000, a no-op under root and on Windows).
	if err := os.MkdirAll(filepath.Dir(storePath(t)), 0o700); err != nil {
		t.Fatalf("mkdir store parent: %v", err)
	}
	if err := os.MkdirAll(storePath(t), 0o700); err != nil {
		t.Fatalf("mkdir store path as directory: %v", err)
	}

	trusted, err := IsTrusted(dir)
	if err == nil {
		t.Fatalf("IsTrusted() error = nil, want non-nil for an unreadable store")
	}
	if trusted {
		t.Fatalf("IsTrusted() = true, want false (fail-closed) on a read error")
	}
}

func TestPersistedFileAndDirModes(t *testing.T) {
	setUserConfigRoot(t)
	dir := t.TempDir()

	if err := Trust(dir); err != nil {
		t.Fatalf("Trust() error = %v", err)
	}

	path := storePath(t)
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := fileInfo.Mode().Perm(); got != 0o600 {
			t.Fatalf("store file mode = %o, want 0600", got)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat store dir: %v", err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("store dir mode = %o, want 0700", got)
		}
	}
}

// storePath returns the on-disk trust store path under the redirected config root.
func storePath(t *testing.T) string {
	t.Helper()
	p, err := storeFilePath()
	if err != nil {
		t.Fatalf("storeFilePath() error = %v", err)
	}
	return p
}
