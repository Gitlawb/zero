//go:build windows

package fsutil

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// protectStaging copies the destination's DACL onto the freshly created staging
// file f before any replacement bytes are written through it. A temporary file
// created in the destination's directory inherits the directory's DACL, not the
// destination's, and os.File.Chmod cannot express a Windows DACL (Go maps only
// the owner-write bit). Without this step the replacement content is readable by
// every principal the directory grants access to during the window between the
// first Write and ReplaceFileW, even though ReplaceFileW later restores the
// restrictive destination DACL onto the published file.
//
// The DACL is read from destPath and written to the staging object. The handle
// os.OpenFile returned for f was opened with GENERIC_READ|GENERIC_WRITE, which
// does not include WRITE_DAC, so a second handle is opened with the write-DAC
// access and checked to name the same object as f before the descriptor is
// applied. That keeps the transfer bound to the object this process created
// instead of a pathname a writer in the directory could redirect.
//
// When the destination's descriptor is protected (its DACL does not inherit),
// the staging DACL is marked protected too, so re-inherited directory ACEs
// cannot widen it. A failure is returned rather than ignored: WriteFileAtomic
// then abandons the staging file and leaves the destination untouched, instead
// of writing content under a broader descriptor.
func protectStaging(f *os.File, destPath string) error {
	descriptor, err := windows.GetNamedSecurityInfo(destPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("fsutil: reading DACL of %s for staging: %w", destPath, err)
	}
	if descriptor == nil {
		return fmt.Errorf("fsutil: destination %s has no security descriptor to protect the staging file", destPath)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		// A destination with no DACL at all is fully permissive, so an inherited
		// staging DACL cannot expose anything the destination does not already.
		if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
			return nil
		}
		return fmt.Errorf("fsutil: extracting DACL of %s for staging: %w", destPath, err)
	}
	if dacl == nil {
		// A present but NULL DACL grants everyone full control: the destination
		// is not restricted, so there is no narrower descriptor to carry over.
		// Setting a NULL DACL here would only widen the staging file.
		return nil
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if control, _, controlErr := descriptor.Control(); controlErr == nil && control&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	handle, err := openStagingForDACL(f)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("fsutil: applying DACL of %s to staging file: %w", destPath, err)
	}
	return nil
}

// openStagingForDACL opens the staging file f names with the READ_CONTROL and
// WRITE_DAC rights SetSecurityInfo requires, which the handle inside f does not
// carry. The new handle is verified to reference the same volume and file index
// as f, and to not be a reparse point, so a directory entry swapped in after
// creation cannot receive the descriptor instead of the object this process
// created. The caller owns the returned handle.
func openStagingForDACL(f *os.File) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("fsutil: opening staging file %s to apply its DACL: %w", f.Name(), err)
	}
	var created, opened windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &created); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("fsutil: querying created staging file %s: %w", f.Name(), err)
	}
	if err := windows.GetFileInformationByHandle(handle, &opened); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("fsutil: querying reopened staging file %s: %w", f.Name(), err)
	}
	if opened.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("fsutil: staging path %s is unexpectedly a reparse point", f.Name())
	}
	if created.VolumeSerialNumber != opened.VolumeSerialNumber ||
		created.FileIndexHigh != opened.FileIndexHigh ||
		created.FileIndexLow != opened.FileIndexLow {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("fsutil: staging path %s no longer names the created file", f.Name())
	}
	return handle, nil
}
