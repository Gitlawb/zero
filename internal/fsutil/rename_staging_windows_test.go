//go:build windows

package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestProtectStagingCopiesRestrictiveDACL pins the Windows pre-publish DACL
// contract: while the replacement bytes are still staged under an inherited
// directory DACL, the staging file must already carry the destination's
// restrictive DACL. A file created in a directory inherits that directory's
// DACL, and Chmod cannot express an owner-only Windows ACL, so without
// protectStaging the content is exposed to every principal the directory grants
// access to even though ReplaceFileW later restores the destination DACL.
func TestProtectStagingCopiesRestrictiveDACL(t *testing.T) {
	dir := t.TempDir()

	// Make the parent more permissive than the destination so an inherited
	// staging DACL is observably broader than the destination's.
	if err := grantEveryoneInheritableDACL(dir); err != nil {
		t.Skipf("cannot widen the parent directory DACL on this filesystem: %v", err)
	}

	target := filepath.Join(dir, "restricted.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	restricted, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;OW)")
	if err != nil {
		t.Skipf("cannot build the restrictive test descriptor: %v", err)
	}
	ownerOnly, _, err := restricted.DACL()
	if err != nil {
		t.Skipf("cannot read the restrictive test DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		target,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, ownerOnly, nil,
	); err != nil {
		t.Skipf("cannot apply a restrictive DACL on this filesystem: %v", err)
	}
	if got, err := readDACLString(target); err != nil {
		t.Skipf("cannot read back the restrictive destination DACL: %v", err)
	} else if !strings.Contains(got, "(A;;FA;;;OW)") {
		t.Skipf("the restrictive DACL did not take effect on this filesystem: %q", got)
	}

	var (
		stagingDACL string
		captureErr  error
	)
	previous := stagingProtectionObserver
	stagingProtectionObserver = func(stagingPath string) {
		stagingDACL, captureErr = readDACLString(stagingPath)
	}
	defer func() { stagingProtectionObserver = previous }()

	if err := WriteFileAtomic(target, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if captureErr != nil {
		t.Fatalf("reading the staging DACL before the write: %v", captureErr)
	}
	if stagingDACL == "" {
		t.Fatal("staging DACL was not observed: the protection hook did not run before the write")
	}
	if !strings.Contains(stagingDACL, "(A;;FA;;;OW)") {
		t.Fatalf("staging DACL = %q, want the owner-only destination DACL", stagingDACL)
	}
	if strings.Contains(stagingDACL, ";;;WD)") {
		t.Fatalf("staging DACL = %q is broader than the destination (inherited Everyone)", stagingDACL)
	}

	after, err := readDACLString(target)
	if err != nil {
		t.Fatalf("reading the destination DACL after replacement: %v", err)
	}
	if !strings.Contains(after, "(A;;FA;;;OW)") || strings.Contains(after, ";;;WD)") {
		t.Fatalf("destination DACL after replacement = %q, want the original owner-only DACL", after)
	}
}

// grantEveryoneInheritableDACL replaces path's DACL with a single inheritable
// grant of full control to Everyone (S-1-1-0), making it broader than any
// owner-only destination inside it.
func grantEveryoneInheritableDACL(path string) error {
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		return err
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}

// readDACLString returns the SDDL DACL portion of path's security descriptor.
func readDACLString(path string) (string, error) {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	if descriptor == nil {
		return "", nil
	}
	text := descriptor.String()
	if index := strings.Index(text, "D:"); index >= 0 {
		return text[index:], nil
	}
	return text, nil
}
