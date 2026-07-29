//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A local Windows account name is capped at 20 characters, so the derived name
// must truncate rather than produce a name NetUserAdd rejects.
func TestWindowsSandboxUserNameRespectsLengthLimit(t *testing.T) {
	name := windowsSandboxUserName(strings.Repeat("a", 64))
	if len(name) > windowsSandboxUserNameMax {
		t.Fatalf("name %q is %d chars, want at most %d", name, len(name), windowsSandboxUserNameMax)
	}
	if !strings.HasPrefix(name, windowsSandboxUserPrefix) {
		t.Fatalf("name %q lost the managed prefix", name)
	}
}

// The same workspace must map to the same principal, otherwise re-running setup
// would accumulate a new local account every time.
func TestWindowsSandboxUserNameIsStable(t *testing.T) {
	first := windowsSandboxUserName("abc123")
	second := windowsSandboxUserName("abc123")
	if first != second {
		t.Fatalf("name is not stable: %q vs %q", first, second)
	}
	if other := windowsSandboxUserName("def456"); other == first {
		t.Fatalf("different workspaces produced the same principal %q", first)
	}
}

// The key is sanitised to characters a local account name accepts, so a hash or
// path fragment cannot smuggle a separator or a space into the name.
func TestWindowsSandboxUserNameRejectsUnsafeCharacters(t *testing.T) {
	name := windowsSandboxUserName(`C:\Users\me\proj ect`)
	for _, r := range strings.TrimPrefix(name, windowsSandboxUserPrefix) {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isDigit {
			t.Fatalf("name %q contains unsafe rune %q", name, r)
		}
	}
	if name == windowsSandboxUserPrefix {
		t.Fatal("sanitising removed every character, leaving a bare prefix")
	}
}

// An empty or fully-sanitised-away key must still yield a usable name rather
// than the bare prefix.
func TestWindowsSandboxUserNameHandlesEmptyKey(t *testing.T) {
	for _, key := range []string{"", "///", "   "} {
		if got := windowsSandboxUserName(key); got == windowsSandboxUserPrefix {
			t.Fatalf("key %q produced a bare prefix", key)
		}
	}
}

// The password must be fresh per call and carry the character classes a default
// Windows complexity policy demands, or NetUserAdd fails with ERROR_PASSWORD_RESTRICTION.
func TestNewWindowsSandboxPasswordIsRandomAndComplex(t *testing.T) {
	first, err := newWindowsSandboxPassword()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	second, err := newWindowsSandboxPassword()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if first == second {
		t.Fatal("two generated passwords are identical, so they are not random")
	}
	if len(first) < 12 {
		t.Fatalf("password is only %d chars", len(first))
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range first {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		t.Fatalf("password %q lacks a required character class", first)
	}
}

// "Already exists" is the normal result of re-running setup and must not surface
// as an error, while a genuine failure must.
func TestNetAPIStatusTreatsExistingAsSuccess(t *testing.T) {
	if err := netAPIStatus("NetUserAdd", nerrSuccess); err != nil {
		t.Fatalf("success status returned %v", err)
	}
	if err := netAPIStatus("NetUserAdd", nerrUserExists, nerrUserExists); err != nil {
		t.Fatalf("existing user must be success, got %v", err)
	}
	if err := netAPIStatus("NetLocalGroupAdd", nerrGroupExists, nerrGroupExists, errorAliasExists); err != nil {
		t.Fatalf("existing group must be success, got %v", err)
	}
	if err := netAPIStatus("NetUserAdd", 2245); err == nil {
		t.Fatal("an unexpected status must surface as an error")
	}
}

// Access-denied is the status an unelevated run gets, and it must say so rather
// than reporting a bare number the user cannot act on.
func TestNetAPIStatusExplainsAccessDenied(t *testing.T) {
	err := netAPIStatus("NetUserAdd", errorAccessDenied32)
	if err == nil {
		t.Fatal("access denied must be an error")
	}
	if !strings.Contains(err.Error(), "elevated") {
		t.Fatalf("error %q should point at elevation", err)
	}
}

// The Win32 structs are passed to netapi32 as raw buffers, so their layout must
// match what the API expects. A wrong size means silent memory corruption.
func TestWindowsIdentityStructLayouts(t *testing.T) {
	ptr := unsafe.Sizeof(uintptr(0))
	if got, want := unsafe.Sizeof(localGroupMembersInfo3{}), ptr; got != want {
		t.Fatalf("LOCALGROUP_MEMBERS_INFO_3 size = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(localGroupInfo1{}), 2*ptr; got != want {
		t.Fatalf("LOCALGROUP_INFO_1 size = %d, want %d", got, want)
	}
	// USER_INFO_1 is four pointers plus three DWORDs, with the compiler padding
	// each DWORD pair up to pointer alignment on amd64.
	if got := unsafe.Sizeof(userInfo1{}); got < 4*ptr {
		t.Fatalf("USER_INFO_1 size = %d, smaller than its four pointer fields", got)
	}
	if unsafe.Offsetof(userInfo1{}.Password) != ptr {
		t.Fatal("USER_INFO_1.Password must directly follow Name")
	}
}

// LSA_UNICODE_STRING counts BYTES, not runes, and excludes the NUL terminator
// from Length while including it in MaximumLength. Getting either wrong makes
// LsaAddAccountRights read past the buffer or silently match no right, so pin it.
func TestNewLSAStringUsesByteLengths(t *testing.T) {
	buffer, err := windows.UTF16FromString("SeBatchLogonRight")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	entry := newLSAString(buffer)
	const runes uint16 = uint16(len("SeBatchLogonRight"))
	if entry.Length != runes*2 {
		t.Fatalf("Length = %d, want %d (bytes, excluding NUL)", entry.Length, runes*2)
	}
	if entry.MaximumLength != (runes+1)*2 {
		t.Fatalf("MaximumLength = %d, want %d (bytes, including NUL)", entry.MaximumLength, (runes+1)*2)
	}
	if entry.Buffer == nil {
		t.Fatal("Buffer must point at the encoded string")
	}
}

// An empty buffer must not produce a struct pointing at nothing with a nonzero
// length, which would hand LSA a wild pointer.
func TestNewLSAStringHandlesEmptyBuffer(t *testing.T) {
	entry := newLSAString(nil)
	if entry.Buffer != nil || entry.Length != 0 || entry.MaximumLength != 0 {
		t.Fatalf("empty buffer produced %+v, want a zero value", entry)
	}
}

// The LSA structs are passed to advapi32 as raw buffers, so their sizes must
// match the Win32 definitions.
func TestLSAStructLayouts(t *testing.T) {
	ptr := unsafe.Sizeof(uintptr(0))
	// LSA_UNICODE_STRING: two uint16 then a pointer, padded to pointer alignment.
	if got, want := unsafe.Sizeof(lsaUnicodeString{}), 2*ptr; got != want {
		t.Fatalf("LSA_UNICODE_STRING size = %d, want %d", got, want)
	}
	if unsafe.Offsetof(lsaUnicodeString{}.Buffer) != ptr {
		t.Fatal("LSA_UNICODE_STRING.Buffer must sit at the second pointer slot")
	}
	var attributes lsaObjectAttributes
	if unsafe.Sizeof(attributes) < 6*ptr-ptr {
		t.Fatalf("LSA_OBJECT_ATTRIBUTES size = %d, smaller than its fields", unsafe.Sizeof(attributes))
	}
	if unsafe.Offsetof(attributes.ObjectName) == 0 {
		t.Fatal("LSA_OBJECT_ATTRIBUTES.ObjectName must not alias Length")
	}
}

// Provisioning creates real local accounts, so it only runs when explicitly
// opted into on an elevated machine. Everything above covers the logic that can
// be exercised without touching the account database.
func TestProvisionWindowsSandboxIdentityRoundTrip(t *testing.T) {
	if os.Getenv("ZERO_WINDOWS_IDENTITY_PROVISION_TEST") != "1" {
		// Spelled out per shell because `set VAR=1` is cmd syntax and silently
		// sets a shell variable rather than an environment variable in
		// PowerShell, which makes this skip look like the elevation check failing.
		t.Skip("provisioning test not enabled: PowerShell `$env:ZERO_WINDOWS_IDENTITY_PROVISION_TEST = \"1\"`, cmd `set ZERO_WINDOWS_IDENTITY_PROVISION_TEST=1`, bash `export ZERO_WINDOWS_IDENTITY_PROVISION_TEST=1` (also needs an elevated terminal)")
	}
	if !windowsProcessIsElevated() {
		t.Skip("provisioning requires an elevated process")
	}
	// A leftover account from an interrupted run is harmless now that
	// provisioning resets the password, but starting clean keeps a failure here
	// from being explained by residue from a previous one.
	_ = removeWindowsSandboxIdentity(windowsSandboxUserName("ziptest01"))

	identity, password, _, err := provisionWindowsSandboxIdentity("ziptest01")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	// Registered immediately after provisioning so every failure path below is
	// covered. This test grants a real batch-logon right to a real local account;
	// leaving either behind on a developer machine is not acceptable residue, and
	// rights are revoked before the account so nothing is left keyed to a SID that
	// no longer resolves.
	t.Cleanup(func() {
		if err := revokeWindowsSandboxLogonRights(identity.SID); err != nil {
			t.Errorf("cleanup: revoke logon rights: %v", err)
		}
		if err := removeWindowsSandboxIdentity(identity.Username); err != nil {
			t.Errorf("cleanup: remove principal: %v", err)
		}
	})
	if identity.SID == nil {
		t.Fatal("provisioned identity has no SID")
	}
	if password == "" {
		t.Fatal("provisioning returned an empty password")
	}
	// Re-running must converge on the same principal rather than failing or
	// creating a second account.
	again, secondPassword, _, err := provisionWindowsSandboxIdentity("ziptest01")
	if err != nil {
		t.Fatalf("second provision: %v", err)
	}
	if again.Username != identity.Username || !again.SID.Equals(identity.SID) {
		t.Fatalf("provisioning is not idempotent: %s then %s", identity, again)
	}
	// The password returned for an account that already existed has to BE that
	// account's password. NetUserAdd leaves an existing account entirely alone,
	// so without an explicit reset this second value is a fresh random string
	// that never authenticates, and the caller would store it as the secret and
	// leave every later command failing to log on with a principal that looks
	// correctly provisioned. Logging on is the only honest way to assert it.
	if secondPassword == "" {
		t.Fatal("second provision returned an empty password")
	}
	if err := grantWindowsSandboxLogonRights(again.SID); err != nil {
		t.Fatalf("grant logon rights: %v", err)
	}
	token, err := logonWindowsSandboxPrincipal(again.Username, secondPassword)
	if err != nil {
		t.Fatalf("logon with the password from the second provision: %v", err)
	}
	_ = token.Close()
	// Lookup must find what provisioning created.
	found, err := lookupWindowsSandboxIdentity("ziptest01")
	if err != nil {
		t.Fatalf("lookup after provision: %v", err)
	}
	if !found.SID.Equals(identity.SID) {
		t.Fatalf("lookup returned %s, want %s", found, identity)
	}
}

// The other half of the privileged chain: granting logon rights and actually
// minting a token. Provisioning proves the account exists; this proves it is
// USABLE, which is the part the runner depends on.
//
// The batch logon doubles as the assertion that LsaAddAccountRights worked. A
// LOGON32_LOGON_BATCH logon fails with ERROR_LOGON_TYPE_NOT_GRANTED (1385)
// unless SeBatchLogonRight is actually held, so a token coming back is proof the
// grant landed rather than merely that the call returned success.
//
// Creates a real local account and removes it again, so it is gated the same way
// as the provisioning round-trip.
func TestGrantLogonRightsAndMintPrincipalToken(t *testing.T) {
	if os.Getenv("ZERO_WINDOWS_IDENTITY_PROVISION_TEST") != "1" {
		t.Skip("provisioning test not enabled: PowerShell `$env:ZERO_WINDOWS_IDENTITY_PROVISION_TEST = \"1\"`, cmd `set ZERO_WINDOWS_IDENTITY_PROVISION_TEST=1`, bash `export ZERO_WINDOWS_IDENTITY_PROVISION_TEST=1` (also needs an elevated terminal)")
	}
	if !windowsProcessIsElevated() {
		t.Skip("granting logon rights requires an elevated process")
	}

	const key = "ziplogon01"
	// A leftover account from an interrupted run would keep its old password,
	// which the freshly generated one will not match, so start from a clean slate.
	_ = removeWindowsSandboxIdentity(windowsSandboxUserName(key))

	identity, password, _, err := provisionWindowsSandboxIdentity(key)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	t.Cleanup(func() {
		// Rights first, then the account: the reverse order strands an LSA entry
		// keyed to a SID that no longer resolves.
		if err := revokeWindowsSandboxLogonRights(identity.SID); err != nil {
			t.Errorf("cleanup: revoke logon rights: %v", err)
		}
		if err := removeWindowsSandboxIdentity(identity.Username); err != nil {
			t.Errorf("cleanup: remove principal: %v", err)
		}
	})

	// Exercises LsaAddAccountRights, including the LSA_UNICODE_STRING byte-length
	// handling that nothing else has run.
	if err := grantWindowsSandboxLogonRights(identity.SID); err != nil {
		t.Fatalf("grant logon rights: %v", err)
	}
	// Idempotent: setup re-runs must not fail on rights the account already holds.
	if err := grantWindowsSandboxLogonRights(identity.SID); err != nil {
		t.Fatalf("granting logon rights twice must succeed: %v", err)
	}

	token, err := logonWindowsSandboxPrincipal(identity.Username, password)
	if err != nil {
		t.Fatalf("logon as principal: %v", err)
	}
	defer token.Close()

	// The token must BE the principal. If this came back as the caller, the whole
	// identity boundary would be an illusion and reads would still run as the user.
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("token user: %v", err)
	}
	if !user.User.Sid.Equals(identity.SID) {
		t.Fatalf("token belongs to %s, want the principal %s", user.User.Sid, identity.SID)
	}
	t.Logf("minted a token for %s", identity)
}

// A workspace with no provisioned principal must report the actionable
// "run setup" error rather than a raw lookup failure, so the command path can
// fall back instead of surfacing a Win32 code.
func TestLookupWindowsSandboxIdentityUnprovisioned(t *testing.T) {
	_, err := lookupWindowsSandboxIdentity("nosuchworkspacekey9z")
	if err == nil {
		t.Skip("a principal for this key unexpectedly exists on this machine")
	}
	if err != errWindowsSandboxIdentityUnavailable {
		t.Fatalf("error = %v, want errWindowsSandboxIdentityUnavailable", err)
	}
}

// A name that resolves to something other than a user account is a conflict,
// not an absent principal, and must not be reported as "setup has not run": the
// command path treats that sentinel as permission to fall back silently, so
// collapsing the two would hide a squatted account behind a quiet downgrade to
// the restricted token.
//
// Every machine already has well-known non-user names to test against, so this
// needs no privilege and no provisioning.
func TestLookupWindowsSandboxIdentityRejectsNonUserAccount(t *testing.T) {
	// Groups that exist on any Windows install. Whichever resolves first is
	// enough; localized machines may not carry the English name.
	for _, group := range []string{"Administrators", "Users", "Guests"} {
		sid, _, accountType, err := windows.LookupSID("", group)
		if err != nil || sid == nil {
			continue
		}
		if accountType == windows.SidTypeUser {
			continue
		}
		resolveErr := func() error {
			_, err := resolveWindowsSandboxSID(group)
			return err
		}()
		if resolveErr == nil {
			t.Fatalf("resolveWindowsSandboxSID(%q) accepted a non-user account (type %d)", group, accountType)
		}
		// The classification is the part that matters: the sentinel is what
		// licenses the command path to fall back silently, so this refusal must
		// survive it rather than be folded into it.
		if classified := classifyWindowsSandboxLookupError(resolveErr); errors.Is(classified, errWindowsSandboxIdentityUnavailable) {
			t.Fatalf("non-user account %q classified as unprovisioned, which would silently downgrade to the restricted token: %v", group, classified)
		}
		return
	}
	t.Skip("no well-known non-user account resolved on this machine")
}

// The account name is derived from a workspace hash, not discovered, so it can
// be occupied by a local account that has nothing to do with Zero. Adopting one
// means resetting a stranger's password, so provisioning has to prove ownership
// first and refuse otherwise.
//
// Driven against real accounts every Windows install carries, which are
// definitively not ours. Unprivileged: it only has to establish that they are
// not classified as managed, so nothing is ever created or modified.
func TestWindowsSandboxUserIsManagedRefusesForeignAccounts(t *testing.T) {
	checked := 0
	for _, name := range []string{"Administrator", "Guest", "DefaultAccount"} {
		managed, err := windowsSandboxUserIsManaged(name, "workspacekey")
		if err != nil {
			// Localized or disabled installs may not carry every one of these.
			continue
		}
		checked++
		if managed {
			t.Fatalf("%q classified as a Zero sandbox principal; provisioning would reset its password", name)
		}
	}
	if checked == 0 {
		t.Skip("no well-known local account could be queried on this machine")
	}
	// An absent account must answer false rather than error, since provisioning
	// asks this question about names that usually do not exist yet.
	managed, err := windowsSandboxUserIsManaged("zero-sbx-nosuchacct", "workspacekey")
	if err != nil {
		t.Fatalf("querying a missing account: %v", err)
	}
	if managed {
		t.Fatal("a missing account was classified as managed")
	}
}

// The refusal has to be a typed, recognisable collision rather than a generic
// failure, so setup can say what is wrong instead of reporting a Win32 status.
func TestWindowsSandboxNameCollisionIsTyped(t *testing.T) {
	wrapped := fmt.Errorf("%w: %q", errWindowsSandboxNameCollision, "zero-sbx-dexample")
	if !errors.Is(wrapped, errWindowsSandboxNameCollision) {
		t.Fatal("collision error does not match its sentinel")
	}
	if !strings.Contains(wrapped.Error(), "not created by Zero") {
		t.Fatalf("collision message = %q, want it to say the account is not ours", wrapped.Error())
	}
}
