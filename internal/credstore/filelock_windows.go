//go:build windows

package credstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// acquireFileLock takes an exclusive OS lock (LockFileEx) so a read-modify-write
// of the credential file is serialized, matching the flock behaviour on unix.
//
// The lock file is SEPARATE from the data file for the same reason: write
// publishes by rename, and a lock on the renamed file would be attached to
// something the next writer has already replaced.
func (s *Store) acquireFileLock(exclusive bool) (func() error, error) {
	file, err := openCredentialLock(s.lockPath())
	if err != nil {
		return nil, err
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	// A fixed 1-byte region. Writers pass LOCKFILE_EXCLUSIVE_LOCK; readers omit
	// it (a shared lock) so they still serialize against a writer's publish —
	// which matters more on Windows, where an unsynchronized reader holding the
	// file open blocks the rename.
	//
	// LOCKFILE_FAIL_IMMEDIATELY plus a deadline rather than a blocking wait: a
	// blocking LockFileEx has no way to report contention, and the sibling
	// provider-write lock in internal/config fails a busy transaction rather
	// than hanging on it. See credentialLockTimeout.
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	deadline := time.Now().Add(credentialLockTimeout)
	for {
		err := windows.LockFileEx(handle, flags, 0, 1, 0, overlapped)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING) {
			_ = file.Close()
			return nil, fmt.Errorf("credstore: lock: %w", err)
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, credentialLockBusyError(file.Name())
		}
		time.Sleep(credentialLockRetryInterval)
	}
	return func() error {
		// Reported rather than swallowed, for the same reason as the unix side: a
		// cleanup that did not complete must not look identical to one that did.
		// It matters more here — on Windows the handle staying open is what blocks
		// the next writer's rename, so a failed release has a visible consequence.
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		closeErr := file.Close()
		if err := errors.Join(unlockErr, closeErr); err != nil {
			return fmt.Errorf("credstore: release lock: %w", err)
		}
		return nil
	}, nil
}

// openCredentialLock traverses from a local volume handle and opens every
// component relative to the preceding handle with FILE_OPEN_REPARSE_POINT.
// This binds containment at use time instead of checking a path and then
// reopening it through a replaceable name.
func openCredentialLock(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("credstore: lock path: %w", err)
	}
	volume := filepath.VolumeName(absolute)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, fmt.Errorf("credstore: unsafe lock path %q is not on a local drive", absolute)
	}
	relative := strings.TrimLeft(absolute[len(volume):], `\/`)
	parts := strings.FieldsFunc(relative, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) < 2 {
		return nil, fmt.Errorf("credstore: unsafe lock path %q has no private parent directory", absolute)
	}

	rootPath, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, fmt.Errorf("credstore: lock volume: %w", err)
	}
	current, err := windows.CreateFile(
		rootPath,
		windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("credstore: open lock volume: %w", err)
	}
	defer func() {
		if current != windows.InvalidHandle {
			_ = windows.CloseHandle(current)
		}
	}()

	for index, part := range parts[:len(parts)-1] {
		next, err := openWindowsPathComponent(current, part, true)
		if err != nil {
			return nil, fmt.Errorf("credstore: open lock directory %q: %w", part, err)
		}
		if err := rejectWindowsReparsePoint(next, absolute); err != nil {
			_ = windows.CloseHandle(next)
			return nil, err
		}
		finalDirectory := index == len(parts)-2
		if err := validateWindowsLockSecurity(next, absolute, true, finalDirectory); err != nil {
			_ = windows.CloseHandle(next)
			return nil, err
		}
		if err := windows.CloseHandle(current); err != nil {
			_ = windows.CloseHandle(next)
			return nil, fmt.Errorf("credstore: close traversed lock directory: %w", err)
		}
		current = next
	}

	lock, err := openWindowsPathComponent(current, parts[len(parts)-1], false)
	if err != nil {
		return nil, fmt.Errorf("credstore: open lock: %w", err)
	}
	if err := rejectWindowsReparsePoint(lock, absolute); err != nil {
		_ = windows.CloseHandle(lock)
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(lock, &info); err != nil {
		_ = windows.CloseHandle(lock)
		return nil, fmt.Errorf("credstore: inspect lock: %w", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || info.NumberOfLinks != 1 {
		_ = windows.CloseHandle(lock)
		return nil, fmt.Errorf("credstore: unsafe lock path %q has unexpected type or link count", absolute)
	}
	if err := validateWindowsLockSecurity(lock, absolute, false, true); err != nil {
		_ = windows.CloseHandle(lock)
		return nil, err
	}
	if err := windows.CloseHandle(current); err != nil {
		_ = windows.CloseHandle(lock)
		return nil, fmt.Errorf("credstore: close lock directory: %w", err)
	}
	current = windows.InvalidHandle
	return os.NewFile(uintptr(lock), absolute), nil
}

func openWindowsPathComponent(parent windows.Handle, name string, directory bool) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
		access |= windows.FILE_LIST_DIRECTORY | windows.FILE_TRAVERSE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
		access |= windows.FILE_READ_DATA | windows.FILE_WRITE_DATA
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtCreateFile(
		&handle,
		access,
		&attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF,
		options,
		0,
		0,
	); err != nil {
		return windows.InvalidHandle, err
	}
	return handle, nil
}

func rejectWindowsReparsePoint(handle windows.Handle, path string) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("credstore: inspect lock path %q: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("credstore: unsafe lock path %q contains a reparse point", path)
	}
	return nil
}

func validateWindowsLockSecurity(handle windows.Handle, path string, directory, strict bool) error {
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("credstore: inspect permissions on %q: %w", path, err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("credstore: inspect owner of %q: %w", path, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("credstore: inspect current user: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("credstore: resolve LocalSystem SID: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("credstore: resolve Administrators SID: %w", err)
	}
	if owner == nil || (!owner.Equals(user.User.Sid) && !owner.Equals(system) && !owner.Equals(administrators)) {
		return fmt.Errorf("credstore: unsafe lock path %q has an untrusted owner", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("credstore: inspect access list on %q: %w", path, err)
	}
	if dacl == nil {
		return fmt.Errorf("credstore: unsafe permissions on %q: no discretionary access list", path)
	}
	dangerous := windows.ACCESS_MASK(windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE)
	if strict {
		dangerous |= windows.GENERIC_ALL | windows.GENERIC_WRITE
		// FILE_ADD_FILE and FILE_ADD_SUBDIRECTORY share the numeric values of
		// FILE_WRITE_DATA and FILE_APPEND_DATA; FILE_DELETE_CHILD is 0x40.
		dangerous |= windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.ACCESS_MASK(0x40)
	} else if directory {
		// On an ancestor, creating siblings is normal (notably in Windows temp
		// directories). Deleting/replacing our traversed child is not.
		dangerous |= windows.ACCESS_MASK(0x40)
	}
	if !directory {
		dangerous |= windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return fmt.Errorf("credstore: inspect access entry on %q: %w", path, err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask&dangerous == 0 {
			continue
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("credstore: unsafe permissions on %q contain an unsupported write-capable access entry", path)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user.User.Sid) || sid.Equals(system) || sid.Equals(administrators) {
			continue
		}
		// A trustee that is not one of the three is not automatically an outage.
		// Real profile and temp directories carry inherited allow-ACEs that
		// Windows put there — capability SIDs under %TEMP%, machine-local account
		// SIDs inherited from the profile ACL — and refusing to touch credentials
		// over one turns hardening into "no credential can be read or written on
		// this box". So the failure is split by what the trustee actually is:
		// a SID naming a CLASS of principals (Everyone, a BUILTIN alias, a logon
		// group) is the dangerous case and still fails closed, while a single
		// unrecognised principal on an object this user owns is reported and
		// allowed.
		if broadLockTrustee(sid) {
			return fmt.Errorf("credstore: unsafe permissions on %q grant write access to untrusted trustee %s", path, sid.String())
		}
		warnUnrecognisedLockTrustee(path, sid)
	}
	return nil
}

// broadLockTrusteeSIDs are trustees that stand for a class of principals rather
// than one account. Write access for any of them means "some other logged-in
// account can rewrite this", which is exactly the case the lock hardening
// exists to refuse.
var broadLockTrusteeSIDs = map[string]struct{}{
	"S-1-1-0":    {}, // Everyone
	"S-1-2-0":    {}, // LOCAL
	"S-1-2-1":    {}, // CONSOLE LOGON
	"S-1-3-0":    {}, // CREATOR OWNER
	"S-1-3-1":    {}, // CREATOR GROUP
	"S-1-5-2":    {}, // NETWORK
	"S-1-5-4":    {}, // INTERACTIVE
	"S-1-5-6":    {}, // SERVICE
	"S-1-5-7":    {}, // ANONYMOUS LOGON
	"S-1-5-11":   {}, // Authenticated Users
	"S-1-5-12":   {}, // RESTRICTED
	"S-1-5-13":   {}, // TERMINAL SERVER USER
	"S-1-5-14":   {}, // REMOTE INTERACTIVE LOGON
	"S-1-5-15":   {}, // This Organization
	"S-1-5-113":  {}, // Local account
	"S-1-5-114":  {}, // Local account and member of Administrators group
	"S-1-5-1000": {}, // Other Organization
}

// broadLockTrustee reports whether sid names a class of principals. Classified
// by SID shape rather than by name lookup: LookupAccountSid can block on a
// domain controller, and this runs on every credential read.
func broadLockTrustee(sid *windows.SID) bool {
	value := sid.String()
	if _, ok := broadLockTrusteeSIDs[value]; ok {
		return true
	}
	switch {
	case strings.HasPrefix(value, "S-1-5-32-"):
		// BUILTIN aliases (Users, Guests, Power Users, ...). Administrators is
		// already accepted above and never reaches here.
		return true
	case strings.HasPrefix(value, "S-1-15-2-"):
		// Application-package groups, notably ALL APPLICATION PACKAGES. Capability
		// SIDs (S-1-15-3-*) are deliberately NOT here: they are ubiquitous on a
		// normal %TEMP% ACL and name a capability, not a set of user accounts.
		return true
	case strings.HasPrefix(value, "S-1-5-21-"):
		// Machine/domain SIDs. RIDs below 1000 are the well-known groups (Domain
		// Users, Domain Guests, ...); real accounts start at 1000.
		fields := strings.Split(value, "-")
		relative, err := strconv.ParseUint(fields[len(fields)-1], 10, 32)
		return err == nil && relative < 1000
	}
	return false
}

// lockTrusteeWarning receives the message for an unrecognised but narrow
// trustee. Replaceable so tests can assert the warning without parsing stderr.
var lockTrusteeWarning = func(message string) {
	fmt.Fprintln(os.Stderr, message)
}

// warnedLockTrustees keeps the warning to once per trustee per process: it is a
// property of the directory ACL, so repeating it on every credential read would
// be noise.
var warnedLockTrustees sync.Map

func warnUnrecognisedLockTrustee(path string, sid *windows.SID) {
	value := sid.String()
	if _, seen := warnedLockTrustees.LoadOrStore(value, struct{}{}); seen {
		return
	}
	lockTrusteeWarning(fmt.Sprintf(
		"credstore: warning: %q grants write access to unrecognised trustee %s; credential operations continue, but review the directory permissions",
		path, value,
	))
}
