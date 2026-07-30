//go:build windows

package sandbox

// Windows sandbox principals.
//
// Every other Windows backend here derives its token from the CALLING user via
// CreateRestrictedToken, which is why the sandbox can constrain writes but not
// reads: a deny ACE that would stop the sandboxed child reading a credential
// store names the same account Zero itself runs as, so it would lock Zero out
// too. Reads therefore stay on the caller's identity and
// credentialDenyReadPaths is a no-op on Windows (#662, #675).
//
// This file provisions a SEPARATE local account per workspace, held in one
// managed local group, so the sandbox has an identity of its own. A deny-read
// ACE naming that principal denies the sandboxed child and nothing else, and
// the same SID is what a firewall rule or a write grant can be keyed to. The
// accounts are created by the elevated `zero sandbox setup` path because
// NetUserAdd requires administrator rights; nothing here runs unelevated.
//
// Provisioning is idempotent: the "already exists" status from each API is a
// success, so setup can be re-run safely and a partially provisioned machine
// converges.

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// windowsSandboxGroupName holds every sandbox principal. Grouping them means
	// an ACE can name the group once instead of enumerating accounts, and it
	// gives setup a single place to find what it previously created.
	windowsSandboxGroupName    = "ZeroSandboxUsers"
	windowsSandboxGroupComment = "Zero sandbox principals (managed by zero sandbox setup)"

	// windowsSandboxUserPrefix keeps the accounts recognisable in `net user` and
	// lets cleanup identify what belongs to Zero. Windows caps a local account
	// name at 20 characters, which windowsSandboxUserName respects.
	windowsSandboxUserPrefix = "zero-sbx-"
	// The comment doubles as the ownership marker AND records which workspace
	// the account belongs to. The account NAME can only carry 11 characters of
	// the workspace digest because of the 20-character local-account limit, so
	// two workspaces whose digests share that prefix derive the same name. The
	// full key here turns that from a silent share of one account, one secret
	// and one ACL identity into a refusal.
	windowsSandboxUserComment    = "Zero sandbox principal (managed)"
	windowsSandboxUserCommentKey = windowsSandboxUserComment + " key="
	windowsSandboxUserNameMax    = 20
)

// Win32 status codes that mean "already there". Treated as success so
// provisioning converges instead of failing on a second run.
const (
	nerrSuccess         = 0
	nerrGroupExists     = 2223
	nerrUserExists      = 2224
	errorAliasExists    = 1379
	errorMemberInAlias  = 1378
	errorAccessDenied32 = 5
	nerrUserNotFound    = 2221
)

// USER_INFO_1 privilege and flag values.
const (
	usrPrivUser           = 1
	ufScript              = 0x0001
	ufNormalAccount       = 0x0200
	ufDontExpirePasswd    = 0x10000
	windowsPasswordLength = 24
)

var (
	netapi32                    = windows.NewLazySystemDLL("netapi32.dll")
	procNetUserAdd              = netapi32.NewProc("NetUserAdd")
	procNetLocalGroupAdd        = netapi32.NewProc("NetLocalGroupAdd")
	procNetLocalGroupAddMembers = netapi32.NewProc("NetLocalGroupAddMembers")
	procNetUserDel              = netapi32.NewProc("NetUserDel")
	procNetUserSetInfo          = netapi32.NewProc("NetUserSetInfo")
	procNetUserGetInfo          = netapi32.NewProc("NetUserGetInfo")
	procNetApiBufferFree        = netapi32.NewProc("NetApiBufferFree")
	procNetUserGetLocalGroups   = netapi32.NewProc("NetUserGetLocalGroups")
)

// userInfo1 mirrors USER_INFO_1. Field order and widths must match the Win32
// struct exactly; it is passed to NetUserAdd as a raw buffer.
type userInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

// userInfo1003 mirrors USER_INFO_1003, the password-only form NetUserSetInfo
// takes when nothing else about the account should change.
type userInfo1003 struct {
	Password *uint16
}

// localGroupInfo1 mirrors LOCALGROUP_INFO_1.
type localGroupInfo1 struct {
	Name    *uint16
	Comment *uint16
}

// localGroupMembersInfo3 mirrors LOCALGROUP_MEMBERS_INFO_3, which identifies a
// member by name rather than SID.
type localGroupMembersInfo3 struct {
	DomainAndName *uint16
}

// windowsSandboxIdentity is a provisioned sandbox principal: the account name
// and the SID that ACEs, tokens and firewall rules are keyed to.
type windowsSandboxIdentity struct {
	Username string
	SID      *windows.SID
}

// String renders the identity for logs without exposing the password, which is
// never stored on this struct.
func (identity windowsSandboxIdentity) String() string {
	if identity.SID == nil {
		return identity.Username
	}
	return identity.Username + " (" + identity.SID.String() + ")"
}

// windowsSandboxUserName derives a stable account name for a workspace key. The
// key is hashed by the caller (see windowsSandboxWorkspaceKey) so the name reveals no
// path, and it is truncated to the 20-character local-account limit. The same
// workspace always maps to the same account, so re-running setup reuses the
// principal instead of accumulating accounts.
func windowsSandboxUserName(workspaceKey string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return -1
		}
	}, workspaceKey)
	if cleaned == "" {
		cleaned = "default"
	}
	name := windowsSandboxUserPrefix + cleaned
	if len(name) > windowsSandboxUserNameMax {
		name = name[:windowsSandboxUserNameMax]
	}
	return name
}

// windowsSandboxUserCommentFor returns the ownership comment for a workspace,
// carrying the full key the account name could only hold 11 characters of.
func windowsSandboxUserCommentFor(workspaceKey string) string {
	return windowsSandboxUserCommentKey + workspaceKey
}

// newWindowsSandboxPassword returns a random password for a sandbox principal.
// The account is never signed into interactively: the password exists only so
// LogonUser can mint a token for it, so it is generated per provisioning run,
// handed straight to the caller, and never persisted by this file. Base32 of
// crypto/rand bytes keeps it alphanumeric, which satisfies complexity policies
// that reject unusual punctuation, and a fixed suffix guarantees the mixed-case
// and digit classes even if the random draw happens to omit one.
func newWindowsSandboxPassword() (string, error) {
	raw := make([]byte, windowsPasswordLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate sandbox password: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	if len(encoded) > windowsPasswordLength {
		encoded = encoded[:windowsPasswordLength]
	}
	return "Zs1!" + encoded, nil
}

// netAPIStatus converts a netapi32 return value into an error, treating the
// supplied status codes as success so callers can spell out which "already
// exists" results are expected.
func netAPIStatus(call string, status uintptr, okStatuses ...uintptr) error {
	if status == nerrSuccess {
		return nil
	}
	for _, ok := range okStatuses {
		if status == ok {
			return nil
		}
	}
	if status == errorAccessDenied32 {
		return fmt.Errorf("%s: access denied (run `zero sandbox setup` from an elevated terminal)", call)
	}
	return fmt.Errorf("%s: status %d", call, status)
}

// ensureWindowsSandboxGroup creates the managed local group, or leaves it alone
// when it already exists.
func ensureWindowsSandboxGroup() error {
	name, err := windows.UTF16PtrFromString(windowsSandboxGroupName)
	if err != nil {
		return err
	}
	comment, err := windows.UTF16PtrFromString(windowsSandboxGroupComment)
	if err != nil {
		return err
	}
	info := localGroupInfo1{Name: name, Comment: comment}
	status, _, _ := procNetLocalGroupAdd.Call(
		0, // local machine
		1, // level: LOCALGROUP_INFO_1
		uintptr(unsafe.Pointer(&info)),
		0,
	)
	// The struct holds pointers into Go memory that the syscall dereferences, so
	// it has to stay reachable until the call has returned.
	runtime.KeepAlive(info)
	runtime.KeepAlive(name)
	runtime.KeepAlive(comment)
	return netAPIStatus("NetLocalGroupAdd", status, nerrGroupExists, errorAliasExists)
}

// ensureWindowsSandboxUser creates a sandbox account with the supplied password.
// The account is a plain local user with no home directory or logon script,
// flagged so its password never expires (nobody is there to rotate it) and so it
// is a normal, enabled account LogonUser can authenticate.
//
// It reports whether the account already existed, because NetUserAdd leaves such
// an account completely untouched, password included. The caller has to reset it
// or the secret it goes on to store would not be the account's password at all.
func ensureWindowsSandboxUser(username string, password string, workspaceKey string) (bool, error) {
	name, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return false, err
	}
	secret, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return false, err
	}
	comment, err := windows.UTF16PtrFromString(windowsSandboxUserCommentFor(workspaceKey))
	if err != nil {
		return false, err
	}
	info := userInfo1{
		Name:     name,
		Password: secret,
		Priv:     usrPrivUser,
		Comment:  comment,
		Flags:    ufScript | ufNormalAccount | ufDontExpirePasswd,
	}
	status, _, _ := procNetUserAdd.Call(
		0, // local machine
		1, // level: USER_INFO_1
		uintptr(unsafe.Pointer(&info)),
		0,
	)
	// The struct holds pointers into Go memory that the call dereferences, so
	// everything it borrows has to outlive the call.
	runtime.KeepAlive(info)
	runtime.KeepAlive(name)
	runtime.KeepAlive(secret)
	runtime.KeepAlive(comment)
	if status == nerrUserExists {
		return true, nil
	}
	return false, netAPIStatus("NetUserAdd", status)
}

// resetWindowsSandboxUserPassword sets the password on an account that already
// existed, so the secret the caller stores is actually the account's password.
//
// Without this, re-running setup produced a fresh random password, wrote it to
// disk, and left the account authenticating with the old one, so every later
// command failed to log on with a principal that looked correctly provisioned.
func resetWindowsSandboxUserPassword(username string, password string) error {
	name, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	secret, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return err
	}
	// USER_INFO_1003 is a password-only update, so nothing else about the
	// account is disturbed.
	info := userInfo1003{Password: secret}
	status, _, _ := procNetUserSetInfo.Call(
		0, // local machine
		uintptr(unsafe.Pointer(name)),
		1003, // level: USER_INFO_1003
		uintptr(unsafe.Pointer(&info)),
		0,
	)
	runtime.KeepAlive(info)
	runtime.KeepAlive(name)
	runtime.KeepAlive(secret)
	return netAPIStatus("NetUserSetInfo", status)
}

// addWindowsSandboxUserToGroup puts a principal in the managed group, ignoring
// the status that means it is already a member.
func addWindowsSandboxUserToGroup(username string) error {
	group, err := windows.UTF16PtrFromString(windowsSandboxGroupName)
	if err != nil {
		return err
	}
	member, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	entry := localGroupMembersInfo3{DomainAndName: member}
	status, _, _ := procNetLocalGroupAddMembers.Call(
		0, // local machine
		uintptr(unsafe.Pointer(group)),
		3, // level: LOCALGROUP_MEMBERS_INFO_3
		uintptr(unsafe.Pointer(&entry)),
		1, // one member
	)
	runtime.KeepAlive(entry)
	runtime.KeepAlive(group)
	runtime.KeepAlive(member)
	return netAPIStatus("NetLocalGroupAddMembers", status, errorMemberInAlias)
}

// errWindowsSandboxNameCollision reports that the derived account name is taken
// by a local account Zero did not create. Setup refuses rather than adopting it.
var errWindowsSandboxPrivilegedAccount = errors.New("the local account matching Zero's derived sandbox name belongs to a privileged group (Administrators, Power Users or Backup Operators); refusing to adopt it as a sandbox principal")

var errWindowsSandboxNameCollision = errors.New("a local account with Zero's derived sandbox name already exists and was not created by Zero")

// windowsSandboxUserIsManaged reports whether a local account is one Zero
// created, by reading back the comment provisioning stamps on it.
//
// This is the gate on adopting an existing account. The name is derived, not
// discovered, so an account can be sitting on it for reasons that have nothing
// to do with Zero, and taking it over means resetting a stranger's password.
//
// A missing account is not managed rather than an error, so callers can use this
// as a plain question without special-casing absence.
func windowsSandboxUserIsManaged(username string, workspaceKey string) (bool, error) {
	name, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return false, err
	}
	var buffer *byte
	status, _, _ := procNetUserGetInfo.Call(
		0, // local machine
		uintptr(unsafe.Pointer(name)),
		1, // level: USER_INFO_1
		uintptr(unsafe.Pointer(&buffer)),
	)
	runtime.KeepAlive(name)
	if status == nerrUserNotFound {
		return false, nil
	}
	if err := netAPIStatus("NetUserGetInfo", status); err != nil {
		return false, err
	}
	if buffer == nil {
		return false, nil
	}
	defer procNetApiBufferFree.Call(uintptr(unsafe.Pointer(buffer)))
	info := (*userInfo1)(unsafe.Pointer(buffer))
	if info.Comment == nil {
		return false, nil
	}
	comment := windows.UTF16PtrToString(info.Comment)
	// An account provisioned before the key was recorded is still ours; it
	// predates this check and cannot be attributed to a workspace, so it is
	// adopted and its comment rewritten on the way through.
	if comment == windowsSandboxUserComment {
		return true, nil
	}
	return comment == windowsSandboxUserCommentFor(workspaceKey), nil
}

// localGroupUsersInfo0 mirrors LOCALGROUP_USERS_INFO_0: one group name pointer.
type localGroupUsersInfo0 struct {
	Name *uint16
}

// windowsSandboxUserIsPrivileged reports whether an account belongs to a local
// group that would make it a poor sandbox principal.
//
// Adoption is the reason this exists. Provisioning will take over an account
// whose name and ownership comment match, and an account that is also in
// Administrators would hand the sandbox exactly the rights the sandbox is meant
// to withhold: it could rewrite the ACLs confining it, read the secret locked to
// the invoking user, and terminate Zero. The name is derived rather than
// discovered, so an account can end up matching without anyone intending it.
//
// Membership is resolved by SID rather than by name so a localised install, where
// the group is called Administrateurs or Administratoren, is still recognised.
func windowsSandboxUserIsPrivileged(username string) (bool, error) {
	name, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return false, err
	}
	var (
		buffer  *byte
		entries uint32
		total   uint32
	)
	status, _, _ := procNetUserGetLocalGroups.Call(
		0, // local machine
		uintptr(unsafe.Pointer(name)),
		0, // level: LOCALGROUP_USERS_INFO_0
		0, // flags: direct membership only
		uintptr(unsafe.Pointer(&buffer)),
		uintptr(^uint32(0)), // MAX_PREFERRED_LENGTH
		uintptr(unsafe.Pointer(&entries)),
		uintptr(unsafe.Pointer(&total)),
	)
	runtime.KeepAlive(name)
	if status == nerrUserNotFound {
		return false, nil
	}
	if err := netAPIStatus("NetUserGetLocalGroups", status); err != nil {
		return false, err
	}
	if buffer == nil || entries == 0 {
		return false, nil
	}
	defer procNetApiBufferFree.Call(uintptr(unsafe.Pointer(buffer)))

	privileged, err := privilegedLocalGroupNames()
	if err != nil {
		return false, err
	}
	groups := unsafe.Slice((*localGroupUsersInfo0)(unsafe.Pointer(buffer)), entries)
	for _, group := range groups {
		if group.Name == nil {
			continue
		}
		if privileged[strings.ToLower(windows.UTF16PtrToString(group.Name))] {
			return true, nil
		}
	}
	return false, nil
}

// privilegedLocalGroupNames resolves the local names of the groups a sandbox
// principal must not belong to. Resolved from well-known SIDs so the comparison
// survives a localised Windows install.
func privilegedLocalGroupNames() (map[string]bool, error) {
	out := map[string]bool{}
	for _, wellKnown := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinBuiltinAdministratorsSid,
		windows.WinBuiltinPowerUsersSid,
		windows.WinBuiltinBackupOperatorsSid,
	} {
		sid, err := windows.CreateWellKnownSid(wellKnown)
		if err != nil {
			// A group this build of Windows does not define is not a membership
			// anyone can hold, so it cannot make an account privileged.
			continue
		}
		account, _, _, err := sid.LookupAccount("")
		if err != nil {
			continue
		}
		out[strings.ToLower(account)] = true
	}
	if len(out) == 0 {
		return nil, errors.New("could not resolve any privileged local group name")
	}
	return out, nil
}

// resolveWindowsSandboxSID looks up the SID for a provisioned principal. The SID
// is the durable handle: account names can collide with a pre-existing local
// user, so every ACE and firewall rule is keyed to the SID rather than the name.
func resolveWindowsSandboxSID(username string) (*windows.SID, error) {
	sid, _, accountType, err := windows.LookupSID("", username)
	if err != nil {
		return nil, fmt.Errorf("look up sandbox principal %q: %w", username, err)
	}
	if accountType != windows.SidTypeUser {
		return nil, fmt.Errorf("sandbox principal %q resolves to a non-user account (type %d)", username, accountType)
	}
	return sid, nil
}

// Indirected so a test can drive provisioning end to end and inject a failure
// at the two points that occur AFTER the account exists. Those are the paths
// whose return value the caller's rollback depends on.
//
// All four are seamed rather than just the last two: every step here needs an
// elevated caller and a real local account, so a test that only replaced the
// post-creation pair would never get past ensureWindowsSandboxGroup on an
// ordinary machine and would pass without reaching the code it names.
var (
	ensureWindowsSandboxGroupFn       = ensureWindowsSandboxGroup
	ensureWindowsSandboxUserFn        = ensureWindowsSandboxUser
	addWindowsSandboxUserToGroupFn    = addWindowsSandboxUserToGroup
	resolveWindowsSandboxSIDFn        = resolveWindowsSandboxSID
	resetWindowsSandboxUserPasswordFn = resetWindowsSandboxUserPassword
	windowsSandboxUserIsManagedFn     = windowsSandboxUserIsManaged
	windowsSandboxUserIsPrivilegedFn  = windowsSandboxUserIsPrivileged
	grantWindowsSandboxLogonRightsFn  = grantWindowsSandboxLogonRights
	revokeWindowsSandboxLogonRightsFn = revokeWindowsSandboxLogonRights
	// applyWindowsACLPlanFn is a seam so a test can pin the ORDER of setup's ACL
	// work. The revocation below only prevents a stale grant if it runs before
	// the plan that re-adds the current one; a test that exercised the revoke
	// helper on its own would pass just as happily with the call site deleted.
	applyWindowsACLPlanFn = applyWindowsACLPlan
)

// provisionWindowsSandboxIdentity ensures the managed group and one sandbox
// principal for workspaceKey exist, and returns the identity plus the password
// the caller needs to mint a token with LogonUser. It is idempotent, so setup
// can run repeatedly.
//
// The password is returned rather than stored, so no credential is written to
// disk here; that happens a layer up where the secret has somewhere safe to
// live. The returned value is always the account's actual password, including
// when the account already existed, because that case is reset explicitly
// below.
func provisionWindowsSandboxIdentity(workspaceKey string) (windowsSandboxIdentity, string, bool, error) {
	if err := ensureWindowsSandboxGroupFn(); err != nil {
		return windowsSandboxIdentity{}, "", false, err
	}
	username := windowsSandboxUserName(workspaceKey)
	password, err := newWindowsSandboxPassword()
	if err != nil {
		return windowsSandboxIdentity{}, "", false, err
	}
	existed, err := ensureWindowsSandboxUserFn(username, password, workspaceKey)
	if err != nil {
		return windowsSandboxIdentity{}, "", false, err
	}
	if existed {
		// Prove the account is ours before touching it. The name is derived from a
		// workspace hash rather than discovered, so it can be occupied by an
		// account that has nothing to do with Zero, whether by coincidence or
		// because somebody created it deliberately. Adopting one means resetting
		// its password, which is not something to do on the strength of a name
		// matching a pattern we generate ourselves.
		managed, err := windowsSandboxUserIsManagedFn(username, workspaceKey)
		if err != nil {
			return windowsSandboxIdentity{}, "", false, err
		}
		if !managed {
			return windowsSandboxIdentity{}, "", false, fmt.Errorf("%w: %q", errWindowsSandboxNameCollision, username)
		}
		// Ours by name and comment is not enough to adopt it. An account that also
		// sits in Administrators (or Power Users, or Backup Operators) would give
		// the sandbox the rights the sandbox exists to withhold: it could rewrite
		// the ACLs confining it, read the secret locked to the invoking user, and
		// stop Zero. Refuse rather than quietly take it over, and say which account
		// so an operator can look at it.
		privileged, err := windowsSandboxUserIsPrivilegedFn(username)
		if err != nil {
			return windowsSandboxIdentity{}, "", false, err
		}
		if privileged {
			return windowsSandboxIdentity{}, "", false, fmt.Errorf("%w: %q", errWindowsSandboxPrivilegedAccount, username)
		}
		// Deliberately NOT resetting the password here.
		//
		// NetUserAdd left an existing account untouched, so the password above is
		// not yet its password and something has to set it. Doing that here, at
		// the top of provisioning, opened a window that lasted until the secret
		// was written several steps later: a failure anywhere in between left a
		// live account whose password nothing on disk knew, and because the
		// account already existed the rollback correctly declined to delete it.
		// The command path then read the absent secret as "not provisioned" and
		// quietly fell back to the weaker backend, so the sandbox was downgraded
		// for good with nothing to show for it.
		//
		// The caller rotates instead, immediately before committing the secret,
		// which narrows that window to a single operation. Until it does, the
		// account keeps its old password and the old secret on disk still
		// authenticates, so a failure before that point costs nothing.
	}
	// Both failures below can happen AFTER NetUserAdd created the account, so the
	// name has to come back with them. The caller's rollback deletes by
	// identity.Username, and returning a zero identity alongside created=true
	// asked it to delete "", which silently stranded the account this run had
	// just made. Group attachment in particular is not a formality: it can fail
	// under local policy, and it is the enforcement boundary, so a half-created
	// principal is exactly the state worth not leaving behind. The SID is absent
	// here, which the rollback already tolerates, since nothing has been granted
	// to it yet.
	if err := addWindowsSandboxUserToGroupFn(username); err != nil {
		return windowsSandboxIdentity{Username: username}, "", !existed, err
	}
	sid, err := resolveWindowsSandboxSIDFn(username)
	if err != nil {
		return windowsSandboxIdentity{Username: username}, "", !existed, err
	}
	return windowsSandboxIdentity{Username: username, SID: sid}, password, !existed, nil
}

// removeWindowsSandboxIdentity deletes a provisioned principal. Callers must
// revoke the principal's ACEs FIRST (see windowsPrincipalRevokePlan): deleting
// the account leaves any surviving ACE naming an unresolvable SID, which is what
// shows up in Explorer as an orphaned entry and is exactly the residue this
// model is meant to avoid.
//
// A missing account is success, so teardown converges the same way provisioning
// does. Requires an elevated caller.
func removeWindowsSandboxIdentity(username string) error {
	name, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	status, _, _ := procNetUserDel.Call(0, uintptr(unsafe.Pointer(name)))
	return netAPIStatus("NetUserDel", status, nerrUserNotFound)
}

// errWindowsSandboxIdentityUnavailable reports that no sandbox principal has
// been provisioned yet, so callers can fall back to the restricted-token
// backend instead of failing the command.
var errWindowsSandboxIdentityUnavailable = errors.New("no Zero sandbox principal is provisioned; run `zero sandbox setup` from an elevated terminal")

// lookupWindowsSandboxIdentity resolves an already-provisioned principal without
// creating anything, so the unelevated command path can discover whether an
// identity exists. It returns errWindowsSandboxIdentityUnavailable when setup
// has not run.
func lookupWindowsSandboxIdentity(workspaceKey string) (windowsSandboxIdentity, error) {
	username := windowsSandboxUserName(workspaceKey)
	// Ownership is checked here as well as at provisioning, because the account
	// NAME cannot carry the whole workspace key.
	//
	// The name keeps 11 characters of the digest; the comment holds all of it.
	// Provisioning refuses a name whose comment names a different workspace, and
	// without the same check here the workspace that LOST that race would still
	// resolve the name to a SID and quietly use the other workspace's principal,
	// its secret and its ACL identity. Setup would have failed for it, so this is
	// the path that decides whether the refusal actually holds.
	//
	// A collision is very unlikely with real keys, roughly 2^-44 per pair, but the
	// cost of being wrong is one workspace running as another's identity, and the
	// check is one syscall on a path that is already doing several.
	// SID resolution runs FIRST so "no such account" stays the unavailable
	// sentinel. windowsSandboxUserIsManaged answers false for both an absent
	// account and one belonging to someone else, so checking it before this would
	// report an unprovisioned workspace as a name collision and turn the ordinary
	// not-set-up case into an error the operator has to interpret.
	sid, err := resolveWindowsSandboxSIDFn(username)
	if err != nil {
		return windowsSandboxIdentity{}, classifyWindowsSandboxLookupError(err)
	}
	managed, err := windowsSandboxUserIsManagedFn(username, workspaceKey)
	if err != nil {
		return windowsSandboxIdentity{}, err
	}
	if !managed {
		return windowsSandboxIdentity{}, fmt.Errorf("%w: %q", errWindowsSandboxNameCollision, username)
	}
	return windowsSandboxIdentity{Username: username, SID: sid}, nil
}

// lookupWindowsSandboxPrincipalForCommand resolves the principal a command will
// actually run as, and refuses one that has since joined a privileged group.
//
// Provisioning already refuses a privileged account, but group membership is not
// frozen at setup: the account can be added to Administrators, Power Users or
// Backup Operators afterwards. Minting a token for it would hand the sandboxed
// command exactly the privileges the sandbox exists to withhold, so the check has
// to run again on the path that mints the token, not only on the path that
// created the account.
//
// Deliberately NOT folded into lookupWindowsSandboxIdentity: teardown resolves
// the same identity to revoke its logon rights before deleting it, and it must
// stay able to clean up an account that has become privileged rather than
// refusing to touch it. Refusing there would leave the very account this guards
// against permanently undeletable by Zero.
func lookupWindowsSandboxPrincipalForCommand(workspaceKey string) (windowsSandboxIdentity, error) {
	identity, err := lookupWindowsSandboxIdentity(workspaceKey)
	if err != nil {
		return windowsSandboxIdentity{}, err
	}
	privileged, err := windowsSandboxUserIsPrivilegedFn(identity.Username)
	if err != nil {
		return windowsSandboxIdentity{}, err
	}
	if privileged {
		return windowsSandboxIdentity{}, fmt.Errorf("%w: %q", errWindowsSandboxPrivilegedAccount, identity.Username)
	}
	return identity, nil
}

// classifyWindowsSandboxLookupError decides whether a failed SID resolution
// means "setup has not run" or "this principal exists but is unusable".
//
// Only "no such account" is the former. Every other failure is a principal the
// caller must not paper over, including the deliberate refusal in
// resolveWindowsSandboxSID of a name squatted by a group or alias. Collapsing
// those into the unavailable sentinel would turn a real conflict into a silent
// fall back to the restricted token, which is exactly the case that should
// reach the operator rather than be absorbed.
//
// Split out from the lookup so the decision can be asserted on its own: the
// lookup derives its account name from a workspace key, so a test cannot hand
// it a name that resolves to a group.
func classifyWindowsSandboxLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_NONE_MAPPED) {
		return errWindowsSandboxIdentityUnavailable
	}
	return err
}
