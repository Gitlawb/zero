//go:build windows

package peermsg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestEnsurePrivateDirAppliesOwnerOnlyProtectedDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero", "peers", "registry")
	if err := ensurePrivateDir(path); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("runtime directory DACL inherits access from its parent")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Equals(user.User.Sid) {
		t.Fatalf("runtime directory owner = %s, want %s", owner.String(), user.User.Sid.String())
	}
	sddl := descriptor.String()
	if !strings.Contains(sddl, user.User.Sid.String()) || strings.Contains(sddl, ";;;WD)") {
		t.Fatalf("runtime directory ACL is not private: %s", sddl)
	}
}

func TestEnsurePrivateDirRejectsWindowsReparseParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create Windows directory symlink: %v", err)
	}
	if err := ensurePrivateDir(filepath.Join(link, "peers")); err == nil {
		t.Fatal("expected reparse-point parent to be rejected")
	}
}

func TestEnsurePrivateDirRejectsWindowsFileComponent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(filepath.Join(file, "peers")); err == nil {
		t.Fatal("expected file path component to be rejected")
	}
}
