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
	windowsSandboxUserPrefix  = "zero-sbx-"
	windowsSandboxUserComment = "Zero sandbox principal (managed)"
	windowsSandboxUserNameMax = 20
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
	// Keep info alive across the call: the struct holds pointers into Go memory
	// that the syscall dereferences.
	defer func() { _ = info }()
	return netAPIStatus("NetLocalGroupAdd", status, nerrGroupExists, errorAliasExists)
}

// ensureWindowsSandboxUser creates a sandbox account with the supplied password,
// or leaves an existing account alone. The account is a plain local user with no
// home directory or logon script, flagged so its password never expires (nobody
// is there to rotate it) and so it is a normal, enabled account LogonUser can
// authenticate.
func ensureWindowsSandboxUser(username string, password string) error {
	name, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	secret, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return err
	}
	comment, err := windows.UTF16PtrFromString(windowsSandboxUserComment)
	if err != nil {
		return err
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
	defer func() { _ = info }()
	return netAPIStatus("NetUserAdd", status, nerrUserExists)
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
	defer func() { _ = entry }()
	return netAPIStatus("NetLocalGroupAddMembers", status, errorMemberInAlias)
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

// provisionWindowsSandboxIdentity ensures the managed group and one sandbox
// principal for workspaceKey exist, and returns the identity plus the password
// the caller needs to mint a token with LogonUser. It is idempotent, so setup
// can run repeatedly.
//
// The password is returned rather than stored: on an account that already
// existed the returned value is the NEW password only if the caller resets it,
// so callers that need to log in must treat a pre-existing account as requiring
// a reset. That is handled a layer up, where the secret has somewhere safe to
// live; keeping it out of this file means no credential is written to disk here.
func provisionWindowsSandboxIdentity(workspaceKey string) (windowsSandboxIdentity, string, error) {
	if err := ensureWindowsSandboxGroup(); err != nil {
		return windowsSandboxIdentity{}, "", err
	}
	username := windowsSandboxUserName(workspaceKey)
	password, err := newWindowsSandboxPassword()
	if err != nil {
		return windowsSandboxIdentity{}, "", err
	}
	if err := ensureWindowsSandboxUser(username, password); err != nil {
		return windowsSandboxIdentity{}, "", err
	}
	if err := addWindowsSandboxUserToGroup(username); err != nil {
		return windowsSandboxIdentity{}, "", err
	}
	sid, err := resolveWindowsSandboxSID(username)
	if err != nil {
		return windowsSandboxIdentity{}, "", err
	}
	return windowsSandboxIdentity{Username: username, SID: sid}, password, nil
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
	sid, err := resolveWindowsSandboxSID(username)
	if err != nil {
		return windowsSandboxIdentity{}, classifyWindowsSandboxLookupError(err)
	}
	return windowsSandboxIdentity{Username: username, SID: sid}, nil
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
