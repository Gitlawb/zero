//go:build !unix && !windows

package fscopy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// On targets that define no platform-native no-follow open primitive (this file
// compiles only under //go:build !unix && !windows — Plan 9, WASM), the
// no-follow contract is "use the native no-follow primitive if available,
// otherwise fail closed". openRegularRead/openRegularWrite therefore ALWAYS
// fail closed rather than run a best-effort TOCTOU-narrowed open that cannot
// atomically refuse a final-component symlink. These tests lock that contract.

// TestNounixOpenRegularReadFailsClosed: no no-follow primitive exists, so a
// read must be refused outright — never return a usable descriptor.
func TestNounixOpenRegularReadFailsClosed(t *testing.T) {
	dir := t.TempDir()
	regular := writeFile(t, dir, "src", "data\n")

	if f, err := openRegularRead(regular); err == nil {
		_ = f.Close()
		t.Fatal("openRegularRead opened a file; want closed failure (no no-follow available)")
	} else if !errors.Is(err, errNoNoFollow) {
		t.Fatalf("openRegularRead returned non-sentinel error: %v", err)
	}
}

// TestNounixOpenRegularWriteFailsClosed: no no-follow primitive exists, so a
// write/truncate must be refused outright — never return a usable descriptor
// and never truncate the destination.
func TestNounixOpenRegularWriteFailsClosed(t *testing.T) {
	dir := t.TempDir()
	existing := writeFile(t, dir, "dst", "preserved-content\n")

	if f, err := openRegularWrite(existing, 0o644); err == nil {
		_ = f.Close()
		t.Fatal("openRegularWrite opened/truncated a file; want closed failure (no no-follow available)")
	} else if !errors.Is(err, errNoNoFollow) {
		t.Fatalf("openRegularWrite returned non-sentinel error: %v", err)
	}

	// The existing regular file must be untouched — fail closed truncates nothing.
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "preserved-content\n" {
		t.Fatalf("existing file was modified despite fail-closed: %q", got)
	}

	// A non-existent destination must NOT be created.
	if _, err := os.Lstat(filepath.Join(dir, "never-created")); !os.IsNotExist(err) {
		t.Fatalf("fail-closed openRegularWrite created a file: %v", err)
	}
}

// TestNounixOpenRegularWriteFailsClosedOnSymlink: a symlink destination must be
// refused and its target never truncated — the core property O_TRUNC-by-path
// violated, here guaranteed trivially by failing before any open.
func TestNounixOpenRegularWriteFailsClosedOnSymlink(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "canary", "preserved\n")
	link := filepath.Join(dir, "dst")
	if err := os.Symlink(filepath.Join(dir, "canary"), link); err != nil {
		t.Skipf("symlink unsupported on this build: %v", err)
	}
	if f, err := openRegularWrite(link, 0o644); err == nil {
		_ = f.Close()
		t.Fatal("openRegularWrite opened a symlink; want closed failure")
	}
	got, err := os.ReadFile(filepath.Join(dir, "canary"))
	if err != nil {
		t.Fatalf("read canary: %v", err)
	}
	if string(got) != "preserved\n" {
		t.Fatalf("symlink target was truncated despite fail-closed: %q", got)
	}
}
