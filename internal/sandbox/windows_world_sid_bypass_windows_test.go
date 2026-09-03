//go:build windows

package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procImpersonateLoggedOnUser = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateLoggedOnUser")

// tokenRestrictedSIDs reads the restricting SIDs the token actually carries.
// Read back from the token rather than from the list handed to the builder,
// because the list is not what the kernel consults.
func tokenRestrictedSIDs(t *testing.T, token windows.Token) []string {
	t.Helper()
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenRestrictedSids, nil, 0, &size)
	if err != nil && err != windows.ERROR_INSUFFICIENT_BUFFER {
		t.Fatalf("size the restricted-SID list: %v", err)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenRestrictedSids, &buffer[0], size, &size); err != nil {
		t.Fatalf("read the restricted-SID list: %v", err)
	}
	groups := (*windows.Tokengroups)(unsafe.Pointer(&buffer[0]))
	out := make([]string, 0, groups.GroupCount)
	for _, group := range groups.AllGroups() {
		out = append(out, group.Sid.String())
	}
	runtime.KeepAlive(buffer)
	return out
}

// protectDirectoryFor replaces the directory's DACL with exactly the grants
// named, detached from inheritance. Everyone-writable share roots and loose
// installer trees look like this, and the inherited ACEs have to go: otherwise
// the caller's own rights answer the first access check for reasons that have
// nothing to do with the restricted-SID list under test.
func protectDirectoryFor(t *testing.T, path string, sids []string) {
	t.Helper()
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(sids))
	for _, value := range sids {
		sid, err := windows.StringToSid(value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatalf("build a DACL for %s: %v", path, err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Skipf("cannot rewrite the DACL of %s here: %v", path, err)
	}
}

// writeAsToken attempts a create inside dir while impersonating token, which is
// the access check this is all about. DISABLE_MAX_PRIVILEGE keeps
// SeChangeNotifyPrivilege, so traversal to dir is bypassed and the answer comes
// from that directory's own DACL rather than from its ancestors'.
func writeAsToken(t *testing.T, token windows.Token, dir string) error {
	t.Helper()
	var impersonation windows.Token
	if err := windows.DuplicateTokenEx(token, windows.TOKEN_ALL_ACCESS, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &impersonation); err != nil {
		t.Fatalf("duplicate the token for impersonation: %v", err)
	}
	defer impersonation.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if result, _, callErr := procImpersonateLoggedOnUser.Call(uintptr(impersonation)); result == 0 {
		t.Fatalf("impersonate the restricted token: %v", callErr)
	}
	defer func() {
		if err := windows.RevertToSelf(); err != nil {
			t.Fatalf("revert to self: %v", err)
		}
	}()
	return os.WriteFile(filepath.Join(dir, "escaped.txt"), []byte("x"), 0o600)
}

// EVERYONE AS A RESTRICTING SID IS A KEY TO EVERY EVERYONE-WRITABLE OBJECT.
//
// A profile with DenyRead selects the token without WRITE_RESTRICTED, and that
// token used to carry the World SID so it could still open its own executable.
// Every principal carries Everyone, so the restricted-SID check then passed for
// free on any path whose DACL grants Everyone write, and the workspace write jail
// fell back to the caller's own permissions. That is the boundary the token
// exists to be stricter than, and share roots opened Everyone:F supply the
// directories.
//
// Driven through a real restricted token and a real impersonated write, because
// the question is what the kernel decides, not what the SID list looks like.
// Rewriting the DACL of a directory this test created needs no Administrator
// rights, so it runs unelevated.
func TestTheStrictTokenCannotWriteAnEveryoneWritableDirectory(t *testing.T) {
	workspace := t.TempDir()
	config := WindowsSandboxCommandConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     workspace,
		WorkspaceRoots: []string{workspace},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: workspace}},
			},
		},
	}
	capability, err := windowsCapabilitySIDForWriteRoot(config, workspace)
	if err != nil {
		t.Fatalf("resolve the workspace capability SID: %v", err)
	}

	// writeRestricted false: the token a DenyRead profile selects.
	token, err := createWindowsRestrictedTokenForCapabilitySIDs([]string{capability}, false)
	if err != nil {
		t.Fatalf("build the strict restricted token: %v", err)
	}
	defer token.Close()

	// The direct fact, before any access check: no universal group is a key.
	restricted := tokenRestrictedSIDs(t, token)
	for _, sid := range restricted {
		if strings.EqualFold(sid, "S-1-1-0") {
			t.Fatalf("the restricted-SID list %v carries Everyone, so every Everyone-writable object on the machine is inside the write jail", restricted)
		}
	}
	if len(restricted) == 0 {
		t.Fatal("SETUP INVALID: the token carries no restricting SIDs at all, so the refusal below would hold for a token that is not restricted")
	}

	// SETUP: the token writes where the capability IS granted, or the refusal
	// below would be satisfied by a token that can write nowhere.
	granted := filepath.Join(workspace, "granted")
	if err := os.MkdirAll(granted, 0o700); err != nil {
		t.Fatal(err)
	}
	protectDirectoryFor(t, granted, []string{"S-1-1-0", capability})
	if err := writeAsToken(t, token, granted); err != nil {
		t.Fatalf("SETUP INVALID: the token cannot write a directory its own capability grants: %v", err)
	}

	// And the bypass: Everyone full control, nothing for the capability.
	everyone := filepath.Join(workspace, "everyone-writable")
	if err := os.MkdirAll(everyone, 0o700); err != nil {
		t.Fatal(err)
	}
	protectDirectoryFor(t, everyone, []string{"S-1-1-0"})
	if err := writeAsToken(t, token, everyone); err == nil {
		t.Fatal("the sandboxed token wrote a directory outside every write root, because its DACL grants Everyone and Everyone is one of the token's restricting SIDs")
	}
}

// The same directory, and the WRITE_RESTRICTED token a profile without DenyRead
// selects: also refused. Both tokens hold the jail, and the flag only scopes
// which accesses the restricted-SID check covers.
func TestTheWriteRestrictedTokenCannotWriteAnEveryoneWritableDirectory(t *testing.T) {
	workspace := t.TempDir()
	config := WindowsSandboxCommandConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     workspace,
		WorkspaceRoots: []string{workspace},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: workspace}},
			},
		},
	}
	capability, err := windowsCapabilitySIDForWriteRoot(config, workspace)
	if err != nil {
		t.Fatalf("resolve the workspace capability SID: %v", err)
	}
	token, err := createWindowsRestrictedTokenForCapabilitySIDs([]string{capability}, true)
	if err != nil {
		t.Fatalf("build the WRITE_RESTRICTED token: %v", err)
	}
	defer token.Close()

	everyone := filepath.Join(workspace, "everyone-writable")
	if err := os.MkdirAll(everyone, 0o700); err != nil {
		t.Fatal(err)
	}
	protectDirectoryFor(t, everyone, []string{"S-1-1-0"})
	if err := writeAsToken(t, token, everyone); err == nil {
		t.Fatal("the sandboxed token wrote a directory outside every write root through an Everyone grant")
	}
}
