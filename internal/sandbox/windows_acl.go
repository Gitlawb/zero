package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type WindowsACLAction string

const (
	WindowsACLAllowWrite WindowsACLAction = "allow-write"
	WindowsACLDenyRead   WindowsACLAction = "deny-read"
	WindowsACLDenyWrite  WindowsACLAction = "deny-write"
	// WindowsACLRevokeCapability removes any existing ACE (allow or deny) for
	// Capability at Path, without itself granting or denying anything (applied
	// via SetEntriesInAclW's SET_ACCESS mode with a zero mask, not
	// REVOKE_ACCESS — see windowsACLAccess for why). It reconciles stale
	// shared/descendant DenyWrite ACEs an earlier setup run applied for the
	// stable read-only capability SID (see BuildWindowsACLPlan) that a later
	// run no longer intends: if a path previously covered by the
	// shared-root/descendant DenyWrite mitigation is later configured as an
	// allowed write root, that old deny is otherwise left on disk and wins
	// over the new Allow under Windows' deny-before-allow evaluation — see
	// jatmn's review. Clearing a SID with no matching ACE is a safe no-op, so
	// this can always be emitted unconditionally alongside every write-root
	// Allow entry.
	WindowsACLRevokeCapability WindowsACLAction = "revoke-capability"
)

type WindowsACLEntry struct {
	Action     WindowsACLAction `json:"action"`
	Path       string           `json:"path"`
	Capability string           `json:"capability"`
	// NoInherit forces the applied ACE to carry no inheritance flags, even
	// when the target is a directory. Without it, applyWindowsACLPlan makes
	// every directory ACE inheritable (SUB_CONTAINERS_AND_OBJECTS_INHERIT),
	// and SetNamedSecurityInfo automatically propagates any inheritable ACE
	// down onto the target's EXISTING descendants (not just new ones it
	// creates going forward) — see the shared-deny-path entries below for
	// why that is unsafe on broad system roots.
	NoInherit   bool `json:"noInherit,omitempty"`
	Materialize bool `json:"materialize,omitempty"`
	// ScanDescendants marks a shared-root DenyWrite entry whose EXISTING
	// writable descendants must ALSO be denied, one direct (non-inheriting)
	// deny per writable descendant, at apply time. A non-inherited deny on the
	// root object alone does not cover a pre-existing child that independently
	// grants Users/Authenticated Users write, because a Windows access check
	// for that child never consults a non-inherited ACE on its parent. This is
	// deliberately NOT serialized (json:"-"): the concrete descendant set is
	// live-filesystem state that differs between the setup process and a later
	// command run, so folding it into the hashed plan would make
	// ValidateWindowsSandboxSetupMarker non-deterministic. The flag itself is
	// derived deterministically from the same inputs on both sides, and the
	// descendant enumeration/denies happen as an apply-time side effect in
	// applyWindowsACLPlan (windows-only), never in the cross-platform plan hash.
	ScanDescendants bool `json:"-"`
	// RevokeDescendants marks a write-root's WindowsACLRevokeCapability entry
	// (see the constant below) as needing the same stale-deny cleanup applied
	// recursively to the root's existing descendants, not just the root path
	// itself. A tree scanned and denied by an earlier setup run (either
	// because it WAS one of the four shared roots, or because it was a
	// writable descendant applyWindowsSharedDescendantDenies found and denied
	// elsewhere in the tree) can later be promoted to an allowed write root by
	// the caller configuring some ANCESTOR of it as a WriteRoot. Revoking only
	// at the exact configured root leaves any stale direct, non-inheriting
	// deny on that ancestor's descendants in place, and a stale deny wins over
	// the newly-added inheritable Allow under Windows' deny-before-allow
	// evaluation — see jatmn's review. Like ScanDescendants, this is
	// deliberately NOT serialized (json:"-"): the concrete stale-deny set is
	// live-filesystem state, and the actual descendant walk/revoke happens as
	// an apply-time side effect in applyWindowsACLPlan (windows-only).
	RevokeDescendants bool `json:"-"`
}

type WindowsACLPlan struct {
	Entries []WindowsACLEntry `json:"entries"`
}

func BuildWindowsACLPlan(config WindowsSandboxCommandConfig) (WindowsACLPlan, error) {
	if config.PermissionProfile.FileSystem.Kind != FileSystemRestricted {
		return WindowsACLPlan{}, errors.New("windows ACL setup requires a restricted filesystem permission profile")
	}
	writeCapabilities, err := windowsWriteRootCapabilities(config)
	if err != nil {
		return WindowsACLPlan{}, err
	}
	var entries []WindowsACLEntry
	for _, capability := range writeCapabilities {
		entries = append(entries, WindowsACLEntry{
			Action:     WindowsACLAllowWrite,
			Path:       capability.Root,
			Capability: capability.SID,
		})
		for _, path := range capability.ProtectedWriteDenyPaths {
			entries = append(entries, WindowsACLEntry{
				Action:     WindowsACLDenyWrite,
				Path:       path,
				Capability: capability.SID,
			})
		}
	}
	writeSIDs := windowsWriteCapabilitySIDs(writeCapabilities)
	for _, path := range config.PermissionProfile.FileSystem.DenyWrite {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		for _, sid := range writeSIDs {
			entries = append(entries, WindowsACLEntry{
				Action:     WindowsACLDenyWrite,
				Path:       path,
				Capability: sid,
			})
		}
	}
	readDenySIDs, err := windowsReadDenyCapabilitySIDs(config, writeSIDs)
	if err != nil {
		return WindowsACLPlan{}, err
	}
	for _, path := range planWindowsDenyReadPaths(config.PermissionProfile.FileSystem.DenyRead) {
		for _, sid := range readDenySIDs {
			entries = append(entries, WindowsACLEntry{
				Action:      WindowsACLDenyRead,
				Path:        path,
				Capability:  sid,
				Materialize: true,
			})
		}
	}

	return WindowsACLPlan{Entries: dedupeWindowsACLEntries(entries)}, nil
}

type windowsWriteRootCapability struct {
	Root                    string
	SID                     string
	ProtectedWriteDenyPaths []string
}

func windowsWriteRootCapabilities(config WindowsSandboxCommandConfig) ([]windowsWriteRootCapability, error) {
	out := make([]windowsWriteRootCapability, 0, len(config.PermissionProfile.FileSystem.WriteRoots))
	for _, root := range config.PermissionProfile.FileSystem.WriteRoots {
		rootPath := strings.TrimSpace(root.Root)
		if rootPath == "" {
			continue
		}
		sid, err := windowsCapabilitySIDForWriteRoot(config, rootPath)
		if err != nil {
			return nil, err
		}
		protected := make([]string, 0, len(root.ProtectedMetadataNames)+len(root.ReadOnlySubpaths))
		for _, subpath := range root.ReadOnlySubpaths {
			if trimmed := strings.TrimSpace(subpath); trimmed != "" {
				protected = append(protected, trimmed)
			}
		}
		for _, name := range root.ProtectedMetadataNames {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				protected = append(protected, filepath.Join(rootPath, trimmed))
			}
		}
		out = append(out, windowsWriteRootCapability{
			Root:                    rootPath,
			SID:                     sid,
			ProtectedWriteDenyPaths: protected,
		})
	}
	return out, nil
}

func windowsCapabilitySIDForWriteRoot(config WindowsSandboxCommandConfig, root string) (string, error) {
	if windowsRootMatchesWorkspace(root, config.WorkspaceRoots) {
		return WindowsWorkspaceCapabilitySID(config.SandboxHome, root)
	}
	return WindowsWritableRootCapabilitySID(config.SandboxHome, root)
}

func windowsWriteCapabilitySIDs(capabilities []windowsWriteRootCapability) []string {
	out := make([]string, 0, len(capabilities))
	seen := map[string]struct{}{}
	for _, capability := range capabilities {
		if capability.SID == "" {
			continue
		}
		if _, ok := seen[capability.SID]; ok {
			continue
		}
		seen[capability.SID] = struct{}{}
		out = append(out, capability.SID)
	}
	return out
}

func windowsReadDenyCapabilitySIDs(config WindowsSandboxCommandConfig, writeSIDs []string) ([]string, error) {
	if len(writeSIDs) > 0 {
		return writeSIDs, nil
	}
	if len(config.PermissionProfile.FileSystem.DenyRead) == 0 {
		return nil, nil
	}
	caps, err := LoadOrCreateWindowsCapabilitySIDs(config.SandboxHome)
	if err != nil {
		return nil, err
	}
	return []string{caps.ReadOnly}, nil
}

func planWindowsDenyReadPaths(paths []string) []string {
	out := make([]string, 0, len(paths)*2)
	seen := map[string]struct{}{}
	push := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		key := windowsCapabilityPathKey(path)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, path)
	}
	for _, path := range paths {
		push(path)
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			push(resolved)
		}
	}
	return out
}

func dedupeWindowsACLEntries(entries []WindowsACLEntry) []WindowsACLEntry {
	out := make([]WindowsACLEntry, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.Action == "" || strings.TrimSpace(entry.Path) == "" || strings.TrimSpace(entry.Capability) == "" {
			continue
		}
		// NoInherit is part of the identity: a direct-only deny and an
		// inheritable one on the same path/SID are different ACL shapes, and
		// collapsing them could silently promote a deliberately non-inherited
		// shared-path deny into an inheritable one (or vice versa).
		key := string(entry.Action) + "\x00" + windowsCapabilityPathKey(entry.Path) + "\x00" + strings.ToLower(entry.Capability) + "\x00" + fmt.Sprintf("%t", entry.NoInherit)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, entry)
	}
	return out
}
