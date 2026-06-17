//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
)

const windowsFileDeleteChild windows.ACCESS_MASK = 0x00000040

type windowsACLPathGroup struct {
	Path        string
	Entries     []WindowsACLEntry
	Materialize bool
}

type windowsACLSnapshot struct {
	Path         string
	Descriptor   *windows.SECURITY_DESCRIPTOR
	Materialized bool
}

func applyWindowsACLPlan(plan WindowsACLPlan) (func() error, error) {
	groups := groupWindowsACLPlanByPath(plan)
	snapshots := make([]windowsACLSnapshot, 0, len(groups))
	for _, group := range groups {
		snapshot, applied, err := applyWindowsACLPathGroup(group)
		if err != nil {
			rollbackErr := rollbackWindowsACLSnapshots(snapshots)
			if rollbackErr != nil {
				return nil, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
			}
			return nil, err
		}
		if applied {
			snapshots = append(snapshots, snapshot)
		}
	}
	return func() error {
		return rollbackWindowsACLSnapshots(snapshots)
	}, nil
}

func groupWindowsACLPlanByPath(plan WindowsACLPlan) []windowsACLPathGroup {
	byPath := map[string]*windowsACLPathGroup{}
	for _, entry := range dedupeWindowsACLEntries(plan.Entries) {
		key := windowsCapabilityPathKey(entry.Path)
		if key == "" {
			continue
		}
		group := byPath[key]
		if group == nil {
			group = &windowsACLPathGroup{Path: entry.Path}
			byPath[key] = group
		}
		group.Entries = append(group.Entries, entry)
		group.Materialize = group.Materialize || entry.Materialize
	}
	out := make([]windowsACLPathGroup, 0, len(byPath))
	for _, group := range byPath {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		return windowsCapabilityPathKey(out[i].Path) < windowsCapabilityPathKey(out[j].Path)
	})
	return out
}

func applyWindowsACLPathGroup(group windowsACLPathGroup) (windowsACLSnapshot, bool, error) {
	path := strings.TrimSpace(group.Path)
	if path == "" || len(group.Entries) == 0 {
		return windowsACLSnapshot{}, false, nil
	}
	materialized := false
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return windowsACLSnapshot{}, false, fmt.Errorf("stat windows ACL target %s: %w", path, err)
		}
		if !group.Materialize {
			if windowsACLGroupRequiresExistingTarget(group) {
				return windowsACLSnapshot{}, false, fmt.Errorf("windows ACL target does not exist: %s", path)
			}
			return windowsACLSnapshot{}, false, nil
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return windowsACLSnapshot{}, false, fmt.Errorf("materialize windows ACL target %s: %w", path, err)
		}
		materialized = true
		info, err = os.Stat(path)
		if err != nil {
			return windowsACLSnapshot{}, false, fmt.Errorf("stat materialized windows ACL target %s: %w", path, err)
		}
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		if materialized {
			_ = os.RemoveAll(path)
		}
		return windowsACLSnapshot{}, false, fmt.Errorf("read windows ACL for %s: %w", path, err)
	}
	oldDACL, _, err := descriptor.DACL()
	if err != nil {
		if materialized {
			_ = os.RemoveAll(path)
		}
		return windowsACLSnapshot{}, false, fmt.Errorf("read windows DACL for %s: %w", path, err)
	}
	accessEntries, err := windowsExplicitAccessEntries(group.Entries, info.IsDir())
	if err != nil {
		if materialized {
			_ = os.RemoveAll(path)
		}
		return windowsACLSnapshot{}, false, err
	}
	nextDACL, err := windows.ACLFromEntries(accessEntries, oldDACL)
	if err != nil {
		if materialized {
			_ = os.RemoveAll(path)
		}
		return windowsACLSnapshot{}, false, fmt.Errorf("build windows ACL for %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, nextDACL, nil); err != nil {
		if materialized {
			_ = os.RemoveAll(path)
		}
		return windowsACLSnapshot{}, false, fmt.Errorf("apply windows ACL for %s: %w", path, err)
	}
	return windowsACLSnapshot{Path: path, Descriptor: descriptor, Materialized: materialized}, true, nil
}

func windowsACLGroupRequiresExistingTarget(group windowsACLPathGroup) bool {
	for _, entry := range group.Entries {
		if entry.Action == WindowsACLAllowWrite {
			return true
		}
	}
	return false
}

func windowsExplicitAccessEntries(entries []WindowsACLEntry, isDir bool) ([]windows.EXPLICIT_ACCESS, error) {
	out := make([]windows.EXPLICIT_ACCESS, 0, len(entries))
	inheritance := uint32(0)
	if isDir {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	for _, entry := range entries {
		sid, err := windows.StringToSid(entry.Capability)
		if err != nil {
			return nil, fmt.Errorf("parse windows capability SID %q: %w", entry.Capability, err)
		}
		accessMode, permissions, err := windowsACLAccess(entry.Action)
		if err != nil {
			return nil, err
		}
		out = append(out, windows.EXPLICIT_ACCESS{
			AccessPermissions: permissions,
			AccessMode:        accessMode,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	return out, nil
}

func windowsACLAccess(action WindowsACLAction) (windows.ACCESS_MODE, windows.ACCESS_MASK, error) {
	switch action {
	case WindowsACLAllowWrite:
		return windows.GRANT_ACCESS, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE, nil
	case WindowsACLDenyRead:
		return windows.DENY_ACCESS, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE, nil
	case WindowsACLDenyWrite:
		return windows.DENY_ACCESS, windows.FILE_GENERIC_WRITE | windows.DELETE | windowsFileDeleteChild | windows.WRITE_DAC | windows.WRITE_OWNER, nil
	default:
		return 0, 0, fmt.Errorf("unsupported windows ACL action %q", action)
	}
}

func rollbackWindowsACLSnapshots(snapshots []windowsACLSnapshot) error {
	var errs []error
	for index := len(snapshots) - 1; index >= 0; index-- {
		snapshot := snapshots[index]
		if snapshot.Materialized {
			if err := os.RemoveAll(snapshot.Path); err != nil {
				errs = append(errs, fmt.Errorf("remove materialized windows ACL target %s: %w", snapshot.Path, err))
			}
			continue
		}
		dacl, _, err := snapshot.Descriptor.DACL()
		if err != nil {
			errs = append(errs, fmt.Errorf("read rollback windows DACL for %s: %w", snapshot.Path, err))
			continue
		}
		if err := windows.SetNamedSecurityInfo(snapshot.Path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			errs = append(errs, fmt.Errorf("rollback windows ACL for %s: %w", snapshot.Path, err))
		}
	}
	return errors.Join(errs...)
}
