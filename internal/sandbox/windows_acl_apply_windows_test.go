//go:build windows

package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A workspace-owned target (no elevation needed: the current user holds WRITE_DAC
// on its own temp dir) must apply and roll back through the handle-based path.
// This exercises the whole open -> GetSecurityInfo -> SetSecurityInfo -> close
// sequence and the re-open rollback that replaced the pathname-based calls in
// #728, so a regression that reintroduced GetNamedSecurityInfo/SetNamedSecurityInfo
// (or broke the handle plumbing) would fail here.
func TestApplyWindowsACLPathGroupHandleBasedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	group := windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLAllowWrite,
			Path:       dir,
			Capability: "S-1-1-0", // Everyone: a well-known, StringToSid-parseable group SID
		}},
	}

	snapshot, applied, err := applyWindowsACLPathGroup(group)
	if err != nil {
		t.Fatalf("applyWindowsACLPathGroup: %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true for an existing directory target")
	}
	if snapshot.Path != dir || snapshot.Materialized {
		t.Fatalf("snapshot = %#v, want Path=%q Materialized=false", snapshot, dir)
	}
	if snapshot.Descriptor == nil {
		t.Fatal("snapshot has no captured descriptor to roll back to")
	}
	if err := rollbackWindowsACLSnapshots([]windowsACLSnapshot{snapshot}); err != nil {
		t.Fatalf("rollbackWindowsACLSnapshots: %v", err)
	}
}

// A materialized target that does not exist yet is created, ACL'd through the
// handle, and removed on rollback.
func TestApplyWindowsACLPathGroupMaterializes(t *testing.T) {
	target := filepath.Join(t.TempDir(), "created")
	group := windowsACLPathGroup{
		Path:        target,
		Materialize: true,
		Entries: []WindowsACLEntry{{
			Action:      WindowsACLDenyRead,
			Path:        target,
			Capability:  "S-1-1-0",
			Materialize: true,
		}},
	}

	snapshot, applied, err := applyWindowsACLPathGroup(group)
	if err != nil {
		t.Fatalf("applyWindowsACLPathGroup: %v", err)
	}
	if !applied || !snapshot.Materialized {
		t.Fatalf("applied=%v materialized=%v, want both true", applied, snapshot.Materialized)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("materialized target not created: %v", err)
	}
	if err := rollbackWindowsACLSnapshots([]windowsACLSnapshot{snapshot}); err != nil {
		t.Fatalf("rollbackWindowsACLSnapshots: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialized target still present after rollback: stat err = %v", err)
	}
}

// The core #728 guard: a target that resolves to a reparse point (symlink /
// junction) is refused rather than followed, so a swapped-in link during elevated
// setup cannot redirect the ACL change onto a system object.
func TestOpenWindowsACLTargetRejectsReparsePoint(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	// Prefer a junction: unlike a symlink it needs no admin/Developer Mode, so
	// this guard actually runs in CI. A junction is a directory reparse point,
	// exactly the swap vector openWindowsACLTarget must refuse to follow. Fall
	// back to a symlink and skip only if neither reparse form can be created.
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, realDir).CombinedOutput(); err != nil {
		if serr := os.Symlink(realDir, link); serr != nil {
			t.Skipf("cannot create a reparse point (junction: %v %q; symlink: %v)", err, strings.TrimSpace(string(out)), serr)
		}
	}
	handle, _, err := openWindowsACLTarget(link)
	if err == nil {
		_ = windows.CloseHandle(handle)
		t.Fatal("openWindowsACLTarget followed a reparse point, want rejection")
	}
	if !strings.Contains(err.Error(), "reparse-point") {
		t.Fatalf("openWindowsACLTarget(symlink) err = %v, want a reparse-point rejection", err)
	}
}

// A missing target surfaces as os.ErrNotExist so the caller's materialize path
// still fires (a real open error, e.g. reparse rejection, must NOT look missing).
func TestOpenWindowsACLTargetMissingIsNotExist(t *testing.T) {
	_, _, err := openWindowsACLTarget(filepath.Join(t.TempDir(), "does-not-exist"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("openWindowsACLTarget(missing) err = %v, want os.ErrNotExist", err)
	}
}

// isDir is read from the same handle used for the ACL ops, not a separate Stat.
func TestOpenWindowsACLTargetReportsIsDir(t *testing.T) {
	dir := t.TempDir()
	handle, isDir, err := openWindowsACLTarget(dir)
	if err != nil {
		t.Fatalf("openWindowsACLTarget(dir): %v", err)
	}
	_ = windows.CloseHandle(handle)
	if !isDir {
		t.Fatal("isDir = false for a directory target, want true")
	}

	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	handle, isDir, err = openWindowsACLTarget(file)
	if err != nil {
		t.Fatalf("openWindowsACLTarget(file): %v", err)
	}
	_ = windows.CloseHandle(handle)
	if isDir {
		t.Fatal("isDir = true for a regular file, want false")
	}
}
