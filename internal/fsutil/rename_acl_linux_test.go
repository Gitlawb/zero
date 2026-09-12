//go:build linux

package fsutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicPreservesRestrictivePOSIXACL(t *testing.T) {
	if _, err := exec.LookPath("setfacl"); err != nil {
		t.Skip("setfacl not on PATH; POSIX ACL preservation is not exercised on this host")
	}
	if _, err := exec.LookPath("getfacl"); err != nil {
		t.Skip("getfacl not on PATH; POSIX ACL preservation is not exercised on this host")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "restricted.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	set := exec.Command("setfacl", "-m", "u:65534:---", target)
	if out, err := set.CombinedOutput(); err != nil {
		t.Skipf("setfacl failed (filesystem may lack ACL support): %v\n%s", err, out)
	}

	before, err := exec.Command("getfacl", "-cp", target).CombinedOutput()
	if err != nil {
		t.Fatalf("getfacl before: %v\n%s", err, before)
	}
	if !namedUserACLDenied(before) {
		t.Skipf("named-user ACL did not stick; getfacl:\n%s", before)
	}

	if err := WriteFileAtomic(target, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q", got, "new")
	}

	after, err := exec.Command("getfacl", "-cp", target).CombinedOutput()
	if err != nil {
		t.Fatalf("getfacl after: %v\n%s", err, after)
	}
	if !namedUserACLDenied(after) {
		t.Fatalf("restrictive named-user ACL was lost after replacement\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func namedUserACLDenied(listing []byte) bool {
	return bytes.Contains(listing, []byte("user:65534:---")) ||
		bytes.Contains(listing, []byte("user:nobody:---")) ||
		bytes.Contains(listing, []byte("user:nfsnobody:---"))
}

func namedUserACLPresent(listing []byte) bool {
	return bytes.Contains(listing, []byte("user:65534:")) ||
		bytes.Contains(listing, []byte("user:nobody:")) ||
		bytes.Contains(listing, []byte("user:nfsnobody:"))
}

func TestWriteFileAtomicDropsInheritedAccessACL(t *testing.T) {
	if _, err := exec.LookPath("setfacl"); err != nil {
		t.Skip("setfacl not on PATH; POSIX ACL inheritance is not exercised on this host")
	}
	if _, err := exec.LookPath("getfacl"); err != nil {
		t.Skip("getfacl not on PATH; POSIX ACL inheritance is not exercised on this host")
	}

	base := t.TempDir()
	dir := filepath.Join(base, "shared")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("setfacl", "-d", "-m", "u:65534:r--", dir).CombinedOutput(); err != nil {
		t.Skipf("setfacl default ACL failed (filesystem may lack ACL support): %v\n%s", err, out)
	}

	target := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(target, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("setfacl", "-b", target).CombinedOutput(); err != nil {
		t.Skipf("setfacl -b failed: %v\n%s", err, out)
	}
	before, err := exec.Command("getfacl", "-cp", target).CombinedOutput()
	if err != nil {
		t.Fatalf("getfacl before: %v\n%s", err, before)
	}
	if namedUserACLPresent(before) {
		t.Skipf("source still has a named-user ACL; cannot assert inheritance removal\ngetfacl:\n%s", before)
	}

	if err := WriteFileAtomic(target, []byte("new"), 0o640); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	after, err := exec.Command("getfacl", "-cp", target).CombinedOutput()
	if err != nil {
		t.Fatalf("getfacl after: %v\n%s", err, after)
	}
	if namedUserACLPresent(after) {
		t.Fatalf("access ACL inherited from the directory default survived the replacement\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
