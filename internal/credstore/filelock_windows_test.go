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

func TestFileLockRejectsWriteAccessForForeignTrustee(t *testing.T) {
	dir := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// NETWORK is deliberately not one of the usual broad principals. Any
	// write-capable allow ACE for a trustee other than the user, SYSTEM, or
	// Administrators must fail closed, not only Everyone/Users-style ACEs.
	sd, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FW;;;NU)", user.User.Sid.String()),
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
	store, err := New(Options{Dir: dir, Storage: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", "secret"); err == nil || !strings.Contains(err.Error(), "untrusted trustee") {
		t.Fatalf("Set error = %v, want foreign trustee rejection", err)
	}
}

// setInheritedDACL replaces dir's DACL with a protected, inheritable one so the
// lock file created inside it inherits exactly these entries. Without OI/CI the
// new file falls back to the token's default DACL, which is machine-specific
// and would make these assertions untestable.
func setInheritedDACL(t *testing.T, dir, entries string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P" + entries)
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

func currentUserSID(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}

// captureLockTrusteeWarnings redirects the warning sink and clears the
// once-per-process dedupe for sid, so the assertion does not depend on whether
// an earlier test already reported the same trustee.
func captureLockTrusteeWarnings(t *testing.T, sid string) *[]string {
	t.Helper()
	warnings := new([]string)
	original := lockTrusteeWarning
	lockTrusteeWarning = func(message string) { *warnings = append(*warnings, message) }
	warnedLockTrustees.Delete(sid)
	t.Cleanup(func() {
		lockTrusteeWarning = original
		warnedLockTrustees.Delete(sid)
	})
	return warnings
}

// A BUILTIN alias is a CLASS of principals: write access for it means any local
// account in that group can rewrite the credential lock. That still fails
// closed, unlike a single unrecognised account below.
func TestFileLockRejectsWriteAccessForBuiltinGroup(t *testing.T) {
	dir := t.TempDir()
	setInheritedDACL(t, dir, fmt.Sprintf("(A;OICI;FA;;;%s)(A;OICI;FW;;;BU)", currentUserSID(t)))
	store, err := New(Options{Dir: dir, Storage: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", "secret"); err == nil || !strings.Contains(err.Error(), "untrusted trustee") {
		t.Fatalf("Set error = %v, want builtin group rejection", err)
	}
}

// THE REGRESSION. Real profile and temp directories carry inherited allow-ACEs
// that Windows or a local tool put there: capability SIDs, machine-local
// accounts, and admin-created local groups (one review machine carried
// "CodexSandboxUsers" on both its profile and temp directories). Failing closed
// on those made every credential operation on such a machine impossible; they
// are now reported and allowed. Only the universal principals still fail
// closed — see TestFileLockRejectsWriteAccessForBuiltinGroup.
func TestFileLockWarnsButProceedsForUnrecognisedAccountTrustee(t *testing.T) {
	const foreign = "S-1-5-21-1111111111-2222222222-3333333333-1234"
	warnings := captureLockTrusteeWarnings(t, foreign)

	dir := t.TempDir()
	setInheritedDACL(t, dir, fmt.Sprintf("(A;OICI;FA;;;%s)(A;OICI;FW;;;%s)", currentUserSID(t), foreign))
	store, err := New(Options{Dir: dir, Storage: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", "secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	secret, ok, err := store.Get("openai")
	if err != nil || !ok || secret != "secret" {
		t.Fatalf("Get = (%q, %v, %v), want the stored secret", secret, ok, err)
	}
	if len(*warnings) == 0 || !strings.Contains((*warnings)[0], foreign) {
		t.Fatalf("warnings = %v, want one naming %s", *warnings, foreign)
	}
	// Once per trustee per process, not once per credential read.
	if got := len(*warnings); got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
}

// Capability SIDs are ubiquitous on a normal %TEMP% ACL and name a capability
// rather than a set of accounts.
func TestFileLockAcceptsCapabilityTrustee(t *testing.T) {
	const capability = "S-1-15-3-1024-1065365936-1281604716-3511738428-1654721687-432734479-3232135806-4053264122-3456934681"
	warnings := captureLockTrusteeWarnings(t, capability)

	dir := t.TempDir()
	setInheritedDACL(t, dir, fmt.Sprintf("(A;OICI;FA;;;%s)(A;OICI;FW;;;%s)", currentUserSID(t), capability))
	store, err := New(Options{Dir: dir, Storage: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", "secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], capability) {
		t.Fatalf("warnings = %v, want one naming the capability SID", *warnings)
	}
}

func TestBroadLockTrusteeClassification(t *testing.T) {
	broad := []string{
		"S-1-1-0",            // Everyone
		"S-1-5-11",           // Authenticated Users
		"S-1-5-32-545",       // BUILTIN\Users
		"S-1-5-32-546",       // BUILTIN\Guests
		"S-1-15-2-1",         // ALL APPLICATION PACKAGES
		"S-1-5-21-1-2-3-513", // Domain Users
	}
	narrow := []string{
		// A local account or an admin-created local group; the two are not
		// distinguishable by SID shape, and both are warned rather than refused.
		"S-1-5-21-1-2-3-1004",
		"S-1-15-3-1024-1065365936", // a capability
		"S-1-5-80-3139157870-2983391045-3678747466-658725712-1809340420", // a service account
	}
	for _, value := range broad {
		sid, err := windows.StringToSid(value)
		if err != nil {
			t.Fatal(err)
		}
		if !broadLockTrustee(sid) {
			t.Errorf("broadLockTrustee(%s) = false, want true", value)
		}
	}
	for _, value := range narrow {
		sid, err := windows.StringToSid(value)
		if err != nil {
			t.Fatal(err)
		}
		if broadLockTrustee(sid) {
			t.Errorf("broadLockTrustee(%s) = true, want false", value)
		}
	}
}
