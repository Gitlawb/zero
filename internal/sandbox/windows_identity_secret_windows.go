//go:build windows

package sandbox

// Storing a sandbox principal's password.
//
// The elevated setup path provisions the account, but the per-command path runs
// UNELEVATED and needs the password to call LogonUser. So the secret has to
// cross that boundary on disk, and the only thing standing between it and the
// sandboxed child is the file's ACL.
//
// The file is locked to the invoking user: an explicit, INHERITANCE-PROTECTED
// DACL granting that user and SYSTEM, and nobody else. The sandbox principal is
// deliberately absent from it, which is the property that matters, because a
// principal that could read this file could mint its own token and the whole
// identity boundary would be decorative. Administrators are not added either;
// an admin can already take ownership, so naming them buys nothing and widens
// the visible grant.
//
// Ordering is load-bearing: the ACL is applied to an EMPTY file before the
// password is written, so the bytes never exist under the directory's inherited
// permissions even briefly.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// windowsSandboxSecretDirName holds per-principal secrets under the Zero config
// directory. Kept in its own directory so the whole set can be removed when the
// sandbox is torn down.
const windowsSandboxSecretDirName = "windows-sandbox"

// windowsSandboxSecretPath returns where a principal's password lives. The
// account name is already sanitised to [a-z0-9-] by windowsSandboxUserName, so
// it cannot escape the directory.
func windowsSandboxSecretPath(configDir string, username string) (string, error) {
	if strings.TrimSpace(configDir) == "" {
		return "", errors.New("windows sandbox secret: empty config directory")
	}
	if strings.TrimSpace(username) == "" {
		return "", errors.New("windows sandbox secret: empty principal name")
	}
	// Defence in depth against a caller passing something windowsSandboxUserName
	// did not produce: refuse anything with a separator or a parent reference.
	if strings.ContainsAny(username, `\/:`) || strings.Contains(username, "..") {
		return "", fmt.Errorf("windows sandbox secret: unsafe principal name %q", username)
	}
	return filepath.Join(configDir, windowsSandboxSecretDirName, username+".secret"), nil
}

// currentTokenUserSID returns the SID of the user this process runs as. Under
// UAC the elevated token keeps the same user SID as the desktop session, so
// setup and the later unelevated command path agree on the owner, which is what
// makes an owner-scoped ACL usable across the elevation boundary.
func currentTokenUserSID() (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get token user: %w", err)
	}
	// The SID points into a buffer owned by the Tokenuser, so copy it out before
	// that buffer goes away.
	copied, err := user.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("copy token user SID: %w", err)
	}
	return copied, nil
}

// lockWindowsSecretToOwner replaces a file's DACL with an explicit,
// inheritance-protected one granting only owner and SYSTEM. PROTECTED is what
// drops any ACE inherited from the config directory; without it a permissive
// parent would still grant access to whoever it names.
func lockWindowsSecretToOwner(path string, owner *windows.SID) error {
	if owner == nil {
		return errors.New("windows sandbox secret: nil owner SID")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve SYSTEM SID: %w", err)
	}
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(owner),
			},
		},
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(system),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build secret ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("lock secret to owner: %w", err)
	}
	return nil
}

// writeWindowsSandboxSecret stores a principal's password readable only by the
// invoking user.
//
// The file is created empty, locked down, and only then written, so the secret
// is never on disk under the directory's inherited ACL. An existing file is
// replaced rather than appended, since a stale password would make LogonUser
// fail in a way that looks like a sandbox bug.
func writeWindowsSandboxSecret(path string, password string) error {
	owner, err := currentTokenUserSID()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create secret directory: %w", err)
	}
	// Truncate any previous secret first: the ACL below is applied to whatever
	// inode ends up at this path, so create it before locking it.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create secret file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close secret file: %w", err)
	}
	if err := lockWindowsSecretToOwner(path, owner); err != nil {
		// Do not leave an unprotected empty file behind.
		_ = os.Remove(path)
		return err
	}
	// Encrypt to the invoking user on top of the ACL, so a copy taken outside the
	// filesystem's enforcement (backup, disk image) is inert. The principal name is
	// the entropy, which keeps one principal's blob from authenticating another.
	sealed, err := protectWindowsSecret(password, windowsSandboxSecretEntropy(path))
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write secret: %w", err)
	}
	return nil
}

// windowsSandboxSecretEntropy derives the DPAPI entropy from the secret's own
// filename, which is the principal name. Deriving it rather than threading the
// name through keeps read and write agreeing by construction.
func windowsSandboxSecretEntropy(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".secret")
}

// Seamed so the permission-denied mapping in readWindowsSandboxSecret is
// testable. Producing a real ERROR_ACCESS_DENIED needs DACL surgery on Windows,
// since a 0000 file is still readable and reading a directory reports
// "Incorrect function", so a test built that way would exercise the platform
// rather than the mapping.
var readWindowsSandboxSecretFile = os.ReadFile

// readWindowsSandboxSecret loads a principal's password. A missing file means
// setup has not run for this workspace, which the caller turns into a fallback
// rather than a hard failure.
func readWindowsSandboxSecret(path string) (string, error) {
	data, err := readWindowsSandboxSecretFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errWindowsSandboxIdentityUnavailable
		}
		// Permission denied is unavailability, not breakage. The secret's DACL
		// names whoever ran setup, so an operator who elevated with a separate
		// administrative account, through runas or an over-the-shoulder UAC
		// prompt, ends up with a secret their ordinary account cannot open. That
		// is the documented fail-soft case: fall back to the restricted token and
		// let the warning say so. Treating it as a hard error instead made every
		// sandboxed command fail on a machine that was merely set up by a
		// different admin, which is a common way to run an elevated setup.
		if os.IsPermission(err) {
			return "", errWindowsSandboxIdentityUnavailable
		}
		return "", fmt.Errorf("read sandbox secret: %w", err)
	}
	if len(data) == 0 {
		return "", errWindowsSandboxIdentityUnavailable
	}
	secret, err := unprotectWindowsSecret(data, windowsSandboxSecretEntropy(path))
	if err != nil {
		// A blob written by another user, for another principal, or by an older
		// build that stored the password in the clear. Report it as unavailable so
		// the caller falls back to the restricted token; the next elevated setup
		// rewrites the secret in the current format.
		return "", errWindowsSandboxIdentityUnavailable
	}
	if strings.TrimSpace(secret) == "" {
		return "", errWindowsSandboxIdentityUnavailable
	}
	return secret, nil
}

// removeWindowsSandboxSecret deletes a stored password. Called before the
// account itself is removed so a secret never outlives the principal it
// authenticates.
// errWindowsSandboxSecretNotOurs reports a secret this account cannot delete
// because another administrator's setup owns its DACL.
//
// Distinguishable so teardown can carry on. The alternative is what it used to
// do: abort before removing the account, its logon rights, its ACEs and its
// ledger, leaving a fully provisioned principal on the machine because one file
// could not be unlinked.
var errWindowsSandboxSecretNotOurs = errors.New("the sandbox secret belongs to a different administrator's setup")

func removeWindowsSandboxSecret(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	// The read path already treats permission-denied as unavailability rather
	// than breakage, for the reason documented on readWindowsSandboxSecret: an
	// operator who elevated with a separate administrative account gets a secret
	// their ordinary account cannot open. Removal has to agree. It did not, so on
	// exactly those machines teardown stopped at the first file and stranded
	// everything after it.
	if os.IsPermission(err) {
		return fmt.Errorf("%w: %s", errWindowsSandboxSecretNotOurs, path)
	}
	return fmt.Errorf("remove sandbox secret: %w", err)
}
