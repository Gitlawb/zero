//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ApplyLandlockFilesystemProfile applies the helper's fallback Linux filesystem
// sandbox to the current process. The caller must exec the final command after
// this returns.
func ApplyLandlockFilesystemProfile(profile PermissionProfile, cwd string) error {
	if err := validateLandlockProfile(profile); err != nil {
		return err
	}
	needsFilesystemRules := profile.FileSystem.Kind != FileSystemUnrestricted
	needsNetworkDeny := profile.Network.Mode == NetworkDeny
	if !needsFilesystemRules && !needsNetworkDeny {
		return nil
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	if needsNetworkDeny {
		if err := ApplyLinuxNetworkDeny(); err != nil {
			return err
		}
	}
	if !needsFilesystemRules {
		return nil
	}
	writeRoots := landlockWritableRoots(profile.FileSystem, cwd)
	if len(writeRoots) == 0 {
		return errors.New("Landlock requires at least one writable root")
	}
	return installLandlockFilesystemRules(writeRoots)
}

func validateLandlockProfile(profile PermissionProfile) error {
	fs := profile.FileSystem
	if fs.Kind != FileSystemRestricted {
		return nil
	}
	if len(fs.DenyRead) > 0 {
		return errors.New("deny-read paths require the bubblewrap helper mode")
	}
	if len(fs.DenyWrite) > 0 {
		return errors.New("deny-write paths require the bubblewrap helper mode")
	}
	for _, root := range fs.WriteRoots {
		if len(root.ReadOnlySubpaths) > 0 || len(root.ProtectedMetadataNames) > 0 {
			return errors.New("read-only write-root subpaths require the bubblewrap helper mode")
		}
	}
	if !landlockHasFullReadAccess(fs) {
		return errors.New("restricted read roots are not supported by Landlock helper mode")
	}
	return nil
}

func landlockHasFullReadAccess(fs FileSystemPolicy) bool {
	if fs.Kind == FileSystemUnrestricted {
		return true
	}
	for _, root := range fs.ReadRoots {
		if filepath.Clean(root) == string(filepath.Separator) {
			return true
		}
	}
	return false
}

func landlockWritableRoots(fs FileSystemPolicy, cwd string) []string {
	roots := make([]string, 0, len(fs.WriteRoots)+1)
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		if pathExists(root) {
			roots = append(roots, filepath.Clean(root))
		}
	}
	add(cwd)
	for _, root := range fs.WriteRoots {
		add(root.Root)
	}
	return dedupeStrings(roots)
}

func installLandlockFilesystemRules(writeRoots []string) error {
	abi, err := queryLandlockABI()
	if err != nil {
		return err
	}
	handledAccess := landlockHandledFilesystemAccess(abi)
	if handledAccess == 0 {
		return errors.New("kernel did not report a usable Landlock filesystem ABI")
	}
	rulesetAttr := unix.LandlockRulesetAttr{Access_fs: handledAccess}
	rulesetFD, err := landlockCreateRuleset(&rulesetAttr, unsafe.Sizeof(rulesetAttr), 0)
	if err != nil {
		return fmt.Errorf("create ruleset: %w", err)
	}
	defer unix.Close(rulesetFD)

	readAccess := landlockReadFilesystemAccess(abi)
	if err := landlockAddPathRule(rulesetFD, string(filepath.Separator), readAccess); err != nil {
		return err
	}
	if pathExists("/dev/null") {
		if err := landlockAddPathRule(rulesetFD, "/dev/null", landlockFileReadWriteAccess(abi)); err != nil {
			return err
		}
	}
	for _, root := range writeRoots {
		if err := landlockAddPathRule(rulesetFD, root, handledAccess); err != nil {
			return err
		}
	}
	if err := landlockRestrictSelf(rulesetFD); err != nil {
		return fmt.Errorf("restrict self: %w", err)
	}
	return nil
}

func queryLandlockABI() (int, error) {
	abi, err := landlockCreateRuleset(nil, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if err != nil {
		return 0, fmt.Errorf("query ABI: %w", err)
	}
	if abi <= 0 {
		return 0, fmt.Errorf("query ABI returned %d", abi)
	}
	return abi, nil
}

func landlockCreateRuleset(attr *unix.LandlockRulesetAttr, size uintptr, flags int) (int, error) {
	var attrPtr uintptr
	if attr != nil {
		attrPtr = uintptr(unsafe.Pointer(attr))
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, attrPtr, size, uintptr(flags))
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func landlockAddPathRule(rulesetFD int, path string, allowedAccess uint64) error {
	pathFD, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open rule path %s: %w", path, err)
	}
	defer unix.Close(pathFD)
	rule := unix.LandlockPathBeneathAttr{
		Allowed_access: allowedAccess,
		Parent_fd:      int32(pathFD),
	}
	_, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD),
		uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(&rule)),
		0,
		0,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("add path rule %s: %w", path, errno)
	}
	return nil
}

func landlockRestrictSelf(rulesetFD int) error {
	_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func landlockHandledFilesystemAccess(abi int) uint64 {
	access := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		access |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		access |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		access |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return access
}

func landlockReadFilesystemAccess(abi int) uint64 {
	access := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR)
	if abi >= 5 {
		access |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return access
}

func landlockFileReadWriteAccess(abi int) uint64 {
	access := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
	if abi >= 3 {
		access |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		access |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return access
}
