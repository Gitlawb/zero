//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsDisableMaxPrivilege = 0x01
	windowsLUAToken            = 0x04
	windowsWriteRestricted     = 0x08
	windowsGroupLogonID        = 0xC0000000
)

var procCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

type windowsLocalSID struct {
	sid *windows.SID
}

func newWindowsLocalSID(value string) (windowsLocalSID, error) {
	ptr, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return windowsLocalSID{}, err
	}
	var sid *windows.SID
	if err := windows.ConvertStringSidToSid(ptr, &sid); err != nil {
		return windowsLocalSID{}, err
	}
	return windowsLocalSID{sid: sid}, nil
}

func (sid windowsLocalSID) close() {
	if sid.sid != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(sid.sid)))
	}
}

func createWindowsRestrictedTokenForCapabilitySIDs(capabilitySIDStrings []string) (windows.Token, error) {
	if len(capabilitySIDStrings) == 0 {
		return 0, errors.New("windows restricted token requires at least one capability SID")
	}
	capabilitySIDs := make([]windowsLocalSID, 0, len(capabilitySIDStrings))
	for _, value := range capabilitySIDStrings {
		sid, err := newWindowsLocalSID(value)
		if err != nil {
			for _, existing := range capabilitySIDs {
				existing.close()
			}
			return 0, fmt.Errorf("parse windows capability SID %q: %w", value, err)
		}
		capabilitySIDs = append(capabilitySIDs, sid)
	}
	defer func() {
		for _, sid := range capabilitySIDs {
			sid.close()
		}
	}()

	var base windows.Token
	desired := uint32(windows.TOKEN_DUPLICATE |
		windows.TOKEN_QUERY |
		windows.TOKEN_ASSIGN_PRIMARY |
		windows.TOKEN_ADJUST_DEFAULT |
		windows.TOKEN_ADJUST_SESSIONID |
		windows.TOKEN_ADJUST_PRIVILEGES)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), desired, &base); err != nil {
		return 0, fmt.Errorf("open process token: %w", err)
	}
	defer base.Close()
	return createWindowsRestrictedTokenFromBase(base, capabilitySIDs)
}

func createWindowsRestrictedTokenFromBase(base windows.Token, capabilitySIDs []windowsLocalSID) (windows.Token, error) {
	logonSID, err := copyWindowsLogonSID(base)
	if err != nil {
		return 0, err
	}
	worldSID, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return 0, fmt.Errorf("create world SID: %w", err)
	}

	entries := make([]windows.SIDAndAttributes, 0, len(capabilitySIDs)+2)
	for _, sid := range capabilitySIDs {
		entries = append(entries, windows.SIDAndAttributes{Sid: sid.sid})
	}
	entries = append(entries,
		windows.SIDAndAttributes{Sid: sidFromBytes(logonSID)},
		windows.SIDAndAttributes{Sid: worldSID},
	)

	var restricted windows.Token
	result, _, callErr := procCreateRestrictedToken.Call(
		uintptr(base),
		uintptr(windowsDisableMaxPrivilege|windowsLUAToken|windowsWriteRestricted),
		0,
		0,
		0,
		0,
		uintptr(len(entries)),
		uintptr(unsafe.Pointer(&entries[0])),
		uintptr(unsafe.Pointer(&restricted)),
	)
	runtime.KeepAlive(logonSID)
	runtime.KeepAlive(entries)
	runtime.KeepAlive(capabilitySIDs)
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return 0, fmt.Errorf("CreateRestrictedToken: %w", callErr)
		}
		return 0, errors.New("CreateRestrictedToken failed")
	}
	if err := enableWindowsTokenPrivilege(restricted, "SeChangeNotifyPrivilege"); err != nil {
		_ = restricted.Close()
		return 0, err
	}
	return restricted, nil
}

func copyWindowsLogonSID(token windows.Token) ([]byte, error) {
	groups, err := token.GetTokenGroups()
	if err == nil {
		if sid := logonSIDFromGroups(groups); sid != nil {
			copied, copyErr := copyWindowsSID(sid)
			runtime.KeepAlive(groups)
			return copied, copyErr
		}
	}
	linked, linkedErr := token.GetLinkedToken()
	if linkedErr == nil {
		defer linked.Close()
		groups, err = linked.GetTokenGroups()
		if err == nil {
			if sid := logonSIDFromGroups(groups); sid != nil {
				copied, copyErr := copyWindowsSID(sid)
				runtime.KeepAlive(groups)
				return copied, copyErr
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read token groups: %w", err)
	}
	return nil, errors.New("logon SID not present on token")
}

func logonSIDFromGroups(groups *windows.Tokengroups) *windows.SID {
	for _, group := range groups.AllGroups() {
		if group.Attributes&windowsGroupLogonID == windowsGroupLogonID {
			return group.Sid
		}
	}
	return nil
}

func copyWindowsSID(sid *windows.SID) ([]byte, error) {
	length := windows.GetLengthSid(sid)
	if length == 0 {
		return nil, errors.New("invalid SID length")
	}
	out := make([]byte, length)
	if err := windows.CopySid(length, sidFromBytes(out), sid); err != nil {
		return nil, err
	}
	return out, nil
}

func sidFromBytes(value []byte) *windows.SID {
	return (*windows.SID)(unsafe.Pointer(&value[0]))
}

func enableWindowsTokenPrivilege(token windows.Token, name string) error {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, namePtr, &luid); err != nil {
		return fmt.Errorf("lookup token privilege %s: %w", name, err)
	}
	privileges := windows.Tokenprivileges{PrivilegeCount: 1}
	privileges.Privileges[0] = windows.LUIDAndAttributes{
		Luid:       luid,
		Attributes: windows.SE_PRIVILEGE_ENABLED,
	}
	if err := windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil); err != nil {
		return fmt.Errorf("enable token privilege %s: %w", name, err)
	}
	return nil
}
