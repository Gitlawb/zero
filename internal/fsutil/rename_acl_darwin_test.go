//go:build darwin

package fsutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileAtomicPreservesOrRefusesNativeACL pins the fail-closed contract
// for a Darwin native (kauth/FILESEC) ACL: replacing the destination either
// carries the ACL over to the new inode, or the replacement is refused and the
// original file is left byte-for-byte intact. What must never happen is a
// successful replacement that silently drops the ACL.
func TestWriteFileAtomicPreservesOrRefusesNativeACL(t *testing.T) {
	if _, err := exec.LookPath("chmod"); err != nil {
		t.Skip("chmod not on PATH")
	}
	if _, err := exec.LookPath("ls"); err != nil {
		t.Skip("ls not on PATH")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "restricted.txt")
	original := "old"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	set := exec.Command("chmod", "+a", "user:nobody deny read", target)
	if out, err := set.CombinedOutput(); err != nil {
		t.Skipf("chmod +a failed (ACLs unavailable on this filesystem): %v\n%s", err, out)
	}

	acl, err := readNativeACL(target)
	if err != nil {
		t.Skipf("cannot read the native ACL that chmod +a just set: %v", err)
	}
	if len(acl) == 0 {
		t.Skip("chmod +a reported success but no native ACL is present")
	}
	before, err := exec.Command("ls", "-le", target).CombinedOutput()
	if err != nil {
		t.Fatalf("ls -le before: %v\n%s", err, before)
	}
	if !strings.Contains(string(before), "deny") {
		t.Skipf("named deny entry did not stick; ls -le:\n%s", before)
	}

	replaceErr := WriteFileAtomic(target, []byte("new"), 0o600)
	if replaceErr != nil {
		// Refusal path: the error is acceptable only if the destination was
		// not mutated on the way out.
		got, rerr := os.ReadFile(target)
		if rerr != nil {
			t.Fatalf("WriteFileAtomic failed (%v) and the original is unreadable: %v", replaceErr, rerr)
		}
		if string(got) != original {
			t.Fatalf("WriteFileAtomic failed (%v) but mutated the destination to %q", replaceErr, got)
		}
		return
	}

	// Preservation path: the replacement succeeded, so the deny entry must
	// still be attached to the new inode.
	after, err := readNativeACL(target)
	if err != nil {
		t.Fatalf("WriteFileAtomic succeeded but the native ACL became unreadable: %v", err)
	}
	if len(after) == 0 {
		t.Fatalf("WriteFileAtomic replaced the destination and lost the native ACL")
	}
	afterListing, err := exec.Command("ls", "-le", target).CombinedOutput()
	if err != nil {
		t.Fatalf("ls -le after: %v\n%s", err, afterListing)
	}
	if !strings.Contains(string(afterListing), "deny") {
		t.Fatalf("native ACL deny entry was lost after replacement\nbefore:\n%s\nafter:\n%s", before, afterListing)
	}
}
