//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const statusDirectoryWriteAccess = windows.ACCESS_MASK(
	windows.GENERIC_ALL |
		windows.GENERIC_WRITE |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_ATTRIBUTES |
		windows.FILE_WRITE_EA |
		0x40, // FILE_DELETE_CHILD
)

var statusReOpenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

// checkStatusDirOwner validates ownership and write access through a handle
// opened beneath root. Path-based ACL inspection would recreate the ancestor
// swap race that Root is intended to close.
func checkStatusDirOwner(root *os.Root, _ os.FileInfo) (returnErr error) {
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open status directory for access validation: %w", err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close status directory access handle: %w", err))
		}
	}()

	raw, err := directory.SyscallConn()
	if err != nil {
		return fmt.Errorf("access status directory handle: %w", err)
	}
	var descriptor *windows.SECURITY_DESCRIPTOR
	var queryErr error
	if err := raw.Control(func(handle uintptr) {
		descriptor, queryErr = windows.GetSecurityInfo(
			windows.Handle(handle),
			windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
		)
	}); err != nil {
		return fmt.Errorf("inspect status directory access: %w", err)
	}
	if queryErr != nil {
		return fmt.Errorf("inspect status directory owner and DACL: %w", queryErr)
	}
	if descriptor == nil {
		return fmt.Errorf("status directory security descriptor is unavailable")
	}

	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("resolve current Windows user: %w", err)
	}
	tokenOwner, err := currentWindowsTokenOwner(token)
	if err != nil {
		return fmt.Errorf("resolve current Windows token owner: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read status directory owner: %w", err)
	}
	if owner == nil || (!owner.Equals(user.User.Sid) && !owner.Equals(tokenOwner)) {
		return fmt.Errorf("status directory is not owned by the current Windows token")
	}

	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read status directory DACL: %w", err)
	}
	if dacl == nil {
		return fmt.Errorf("status directory has an unrestricted Windows DACL")
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("read status directory DACL entry %d: %w", index, err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return fmt.Errorf("status directory DACL entry %d has unsupported type %d", index, ace.Header.AceType)
		}
		if ace.Mask&statusDirectoryWriteAccess == 0 {
			continue
		}
		trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !allowedStatusDirectoryTrustee(trustee, user.User.Sid) {
			return fmt.Errorf("status directory DACL grants write access to unexpected trustee %s", trustee.String())
		}
	}
	return nil
}

func hardenStatusDir(root *os.Root) (returnErr error) {
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open status directory for hardening: %w", err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close status directory hardening handle: %w", err))
		}
	}()

	raw, err := directory.SyscallConn()
	if err != nil {
		return fmt.Errorf("access status directory hardening handle: %w", err)
	}
	var securityHandle windows.Handle
	var reopenErr error
	if err := raw.Control(func(rawHandle uintptr) {
		reopened, _, callErr := statusReOpenFile.Call(
			rawHandle,
			uintptr(windows.READ_CONTROL|windows.WRITE_DAC),
			uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE),
			uintptr(windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS),
		)
		securityHandle = windows.Handle(reopened)
		if securityHandle == windows.InvalidHandle {
			reopenErr = callErr
		}
	}); err != nil {
		return fmt.Errorf("reopen status directory hardening handle: %w", err)
	}
	if reopenErr != nil {
		return fmt.Errorf("reopen status directory with security access: %w", reopenErr)
	}
	if securityHandle == 0 || securityHandle == windows.InvalidHandle {
		return fmt.Errorf("reopen status directory with security access: invalid handle")
	}
	defer func() {
		if err := windows.CloseHandle(securityHandle); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close status directory security handle: %w", err))
		}
	}()

	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("resolve current Windows user: %w", err)
	}
	tokenOwner, err := currentWindowsTokenOwner(token)
	if err != nil {
		return fmt.Errorf("resolve current Windows token owner: %w", err)
	}
	desired, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("O:%sD:P(A;OICI;GA;;;%s)(A;OICI;GA;;;SY)", user.User.Sid.String(), user.User.Sid.String()),
	)
	if err != nil {
		return fmt.Errorf("build private status directory DACL: %w", err)
	}
	dacl, _, err := desired.DACL()
	if err != nil {
		return fmt.Errorf("read private status directory DACL: %w", err)
	}

	current, err := windows.GetSecurityInfo(securityHandle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read status directory owner before hardening: %w", err)
	}
	owner, _, err := current.Owner()
	if err != nil {
		return fmt.Errorf("read status directory owner before hardening: %w", err)
	}
	if owner == nil || (!owner.Equals(user.User.Sid) && !owner.Equals(tokenOwner)) {
		return fmt.Errorf("status directory is not owned by the current Windows token")
	}
	if err := windows.SetSecurityInfo(
		securityHandle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("harden status directory DACL: %w", err)
	}
	return nil
}

type statusDirectoryTokenOwner struct {
	owner *windows.SID
}

func currentWindowsTokenOwner(token windows.Token) (*windows.SID, error) {
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &size)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, err
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], size, &size); err != nil {
		return nil, err
	}
	owner := (*statusDirectoryTokenOwner)(unsafe.Pointer(&buffer[0])).owner
	if owner == nil {
		return nil, errors.New("Windows access token has no default owner")
	}
	return owner.Copy()
}

func allowedStatusDirectoryTrustee(trustee, user *windows.SID) bool {
	return trustee != nil && (trustee.Equals(user) ||
		trustee.IsWellKnown(windows.WinLocalSystemSid) ||
		trustee.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
		trustee.IsWellKnown(windows.WinCreatorOwnerSid) ||
		trustee.IsWellKnown(windows.WinCreatorOwnerRightsSid))
}
