//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// runtimeDescentBarrier, when set, runs after the base directory has been
// opened and before the first owned component is touched. It exists so a test
// can swap an owned component for a junction at exactly the point the old
// pathname walk was vulnerable, and prove the redirected target is never
// created or granted. Nil in production.
var runtimeDescentBarrier func()

// createRuntimeTailHandleRelative creates the owned tail of the runtime root
// beneath base, one component at a time, from retained handles.
//
// THE NAME IS NOT AUTHORIZATION. The previous shape found the deepest existing
// ancestor with os.Stat and then created each missing component by opening its
// parent BY NAME. Both follow a junction, so a local user could replace the
// predictable owned "zero" component with a junction after the pre-check,
// point it anywhere, let elevated setup create runtime\v1\<hash> beneath that
// target, and put the original back before the post-check. The post-check then
// saw an ordinary path, the plan applied normally, and rollback reported the
// separately created target only as identity-mismatched residue.
//
// So the base is opened by name exactly once, and everything below it is
// addressed relative to a handle with FILE_OPEN_REPARSE_POINT: an existing
// component is opened no-follow and refused if it is a link, and a missing one
// is created relative to its parent's handle and identified from the handle the
// create returned. No component name is resolved twice, so there is no interval
// for a swap to land in, and a junction placed at any owned component is seen
// as the link it is rather than followed.
//
// Redirected cache and TEMP locations ABOVE the owned tail are still allowed:
// they are part of base, which is the operator's business. The restriction is
// on the zero/runtime/v1/<hash> components Zero itself owns.
func createRuntimeTailHandleRelative(base string, tail []string) ([]windowsCreatedRuntimeDir, error) {
	parent, err := openWindowsDirectoryByName(base)
	if err != nil {
		return nil, fmt.Errorf("open sandbox runtime base %s: %w", base, err)
	}
	defer func() { _ = windows.CloseHandle(parent) }()

	if runtimeDescentBarrier != nil {
		runtimeDescentBarrier()
	}

	var created []windowsCreatedRuntimeDir
	for _, path := range tail {
		name := filepath.Base(path)
		// Exists already: another process won the race, or it was there before
		// setup began. Opened no-follow, so a junction here is refused rather than
		// descended, and it is not ours to record.
		existing, openErr := openWindowsChildNoFollow(parent, name,
			windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, windows.FILE_DIRECTORY_FILE)
		if openErr == nil {
			_ = windows.CloseHandle(parent)
			parent = existing
			continue
		}
		if !isWindowsNotFound(openErr) {
			return created, fmt.Errorf("inspect sandbox runtime component %s: %w", path, openErr)
		}
		handle, createErr := createWindowsChildDirectory(parent, name)
		if createErr != nil {
			if errors.Is(createErr, windows.STATUS_OBJECT_NAME_COLLISION) {
				// Created between the open and the create by someone else. Same
				// answer as the os.IsExist branch the caller already had: not ours,
				// but not a failure either. Reopen no-follow so the descent
				// continues through a verified object rather than a name.
				existing, reopenErr := openWindowsChildNoFollow(parent, name,
					windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, windows.FILE_DIRECTORY_FILE)
				if reopenErr != nil {
					return created, fmt.Errorf("inspect sandbox runtime component %s after a concurrent create: %w", path, reopenErr)
				}
				_ = windows.CloseHandle(parent)
				parent = existing
				continue
			}
			return created, fmt.Errorf("create sandbox runtime root %s: %w", path, createErr)
		}
		identity, idErr := handleRuntimeIdentity(handle)
		if idErr != nil {
			_ = windows.CloseHandle(handle)
			return created, fmt.Errorf("identify the sandbox runtime directory created at %s: %w", path, idErr)
		}
		created = append(created, windowsCreatedRuntimeDir{path: path, identity: identity, identified: true})
		_ = windows.CloseHandle(parent)
		parent = handle
	}
	return created, nil
}
