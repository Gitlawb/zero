//go:build windows

package peermsg

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ensurePrivateDir(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	descriptor, userSID, err := privateDirectoryDescriptor()
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	parent, err := openWindowsDirectory(current, windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.SYNCHRONIZE)
	if err != nil {
		return err
	}
	defer func() {
		if parent != 0 {
			_ = windows.CloseHandle(parent)
		}
	}()
	components := strings.Split(strings.TrimPrefix(abs[len(volume):], string(filepath.Separator)), string(filepath.Separator))
	for index, component := range components {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_TRAVERSE | windows.SYNCHRONIZE)
		if index == len(components)-1 {
			access |= windows.READ_CONTROL | windows.WRITE_DAC
		}
		next, openErr := openOrCreatePrivateWindowsDirectory(parent, component, access, descriptor)
		if openErr != nil {
			return fmt.Errorf("open private runtime path component %q: %w", current, openErr)
		}
		_ = windows.CloseHandle(parent)
		parent = next
	}
	if err := securePrivateDirectory(parent, abs, descriptor, userSID); err != nil {
		return err
	}
	_ = windows.CloseHandle(parent)
	parent = 0
	return nil
}

func privateDirectoryDescriptor() (*windows.SECURITY_DESCRIPTOR, *windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve current Windows user: %w", err)
	}
	sid := user.User.Sid
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;OICI;GA;;;%s)(A;OICI;GA;;;SY)", sid.String(), sid.String()))
	if err != nil {
		return nil, nil, fmt.Errorf("build private Windows directory ACL: %w", err)
	}
	return descriptor, sid, nil
}

func openWindowsDirectory(path string, access uint32) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("open private runtime directory %q: %w", path, err)
	}
	return handle, nil
}

func openOrCreatePrivateWindowsDirectory(parent windows.Handle, name string, access uint32, descriptor *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	objectAttributes := &windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      parent,
		ObjectName:         objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: descriptor,
	}
	var handle windows.Handle
	err = windows.NtCreateFile(
		&handle,
		access,
		objectAttributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return 0, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("refusing non-directory or reparse-point component %q", name)
	}
	return handle, nil
}

func securePrivateDirectory(handle windows.Handle, path string, desired *windows.SECURITY_DESCRIPTOR, userSID *windows.SID) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("inspect private runtime directory %q: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("refusing non-directory or reparse-point runtime path %q", path)
	}
	current, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read owner of private runtime directory %q: %w", path, err)
	}
	owner, _, err := current.Owner()
	if err != nil {
		return fmt.Errorf("read owner of private runtime directory %q: %w", path, err)
	}
	if !owner.Equals(userSID) {
		return fmt.Errorf("refusing runtime directory %q not owned by the current user", path)
	}
	dacl, _, err := desired.DACL()
	if err != nil {
		return fmt.Errorf("read private Windows directory DACL: %w", err)
	}
	if err := windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("secure private runtime directory %q: %w", path, err)
	}
	return nil
}
