//go:build windows

package credstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func secureTestDirectory(t *testing.T, dir string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;%s)", user.User.Sid.String()),
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
}

func TestFileLockRejectsReparsePointRedirection(t *testing.T) {
	dir := t.TempDir()
	store := fileStore(t, dir)
	target := filepath.Join(t.TempDir(), "outside.lock")
	const sentinel = "outside"
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.lockPath()); err != nil {
		t.Skipf("creating a Windows symlink requires Developer Mode or SeCreateSymbolicLinkPrivilege: %v", err)
	}

	if err := store.Set("openai", "secret"); err == nil || !strings.Contains(err.Error(), "unsafe lock path") {
		t.Fatalf("Set error = %v, want unsafe lock path rejection", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != sentinel {
		t.Fatalf("redirect target = %q, err %v; want unchanged", data, err)
	}
	if _, err := os.Stat(store.file); !os.IsNotExist(err) {
		t.Fatalf("credential file exists after rejected lock: %v", err)
	}
}

func TestFileLockRejectsUnsafeParentPermissions(t *testing.T) {
	dir := t.TempDir()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	store, err := New(Options{Dir: dir, Storage: "file"})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Set("openai", "secret"); err == nil || !strings.Contains(err.Error(), "unsafe permissions") {
		t.Fatalf("Set error = %v, want unsafe permissions rejection", err)
	}
	if _, err := os.Stat(store.file); !os.IsNotExist(err) {
		t.Fatalf("credential file exists after rejected parent: %v", err)
	}
}
