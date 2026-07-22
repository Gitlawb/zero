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

	// Deny write to shared Windows-writable directories (C:\, C:\ProgramData,
	// C:\Windows\Temp, C:\Users\Public) to prevent write-jail escape via the
	// added Users and Authenticated Users SIDs. Only DenyRead profiles on the
	// elevated tier (WindowsSandboxLevelRestrictedToken, applied by `zero
	// sandbox setup` running as Administrator) carry those SIDs at all: a
	// WRITE_RESTRICTED token reads with its normal identity and is never
	// broadened, so the default profile needs no shared entries, and only
	// the elevated tier has the WRITE_DAC needed to edit these system-owned
	// DACLs. The unelevated tier keeps the narrower restricting-SID set and
	// never needs these entries either.
	if config.SandboxLevel == WindowsSandboxLevelRestrictedToken && len(config.PermissionProfile.FileSystem.DenyRead) > 0 {
		// Resolved from trusted Win32 APIs, not from the
		// SystemDrive/SystemRoot/ProgramData/PUBLIC environment variables:
		// see resolveWindowsSharedDenyPaths for why trusting the environment
		// here would be a spoofable security boundary.
		systemDrive, systemRoot, programData, publicDir, err := resolveWindowsSharedDenyPaths()
		if err != nil {
			return WindowsACLPlan{}, fmt.Errorf("resolve shared deny paths: %w", err)
		}

		sharedDenyPaths := []string{
			systemDrive + `\`,
			programData,
			systemRoot + `\Temp`,
			publicDir,
		}

		// The deny ACEs name only the stable read-only capability SID, which
		// every broadened token carries (see the runner): a deny ACE blocks
		// when it matches ANY SID on the token, so one shared identity is
		// sufficient, and it keeps these machine-wide DACLs at a constant
		// four entries total. Naming the per-workspace/per-root capability
		// SIDs here instead would append four permanent deny ACEs for every
		// distinct project ever sandboxed on the machine, growing C:\,
		// ProgramData, Windows\Temp, and Public's DACLs without bound.
		caps, err := LoadOrCreateWindowsCapabilitySIDs(config.SandboxHome)
		if err != nil {
			return WindowsACLPlan{}, err
		}
		denySID := caps.ReadOnly

		for _, denyPath := range sharedDenyPaths {
			if windowsPathUnderAnyRoot(denyPath, writeCapabilities) {
				continue // Do not deny write if it IS or is nested under an allowed write root
			}
			// NoInherit: these four shared paths must NOT carry an inheritable
			// ACE. SetNamedSecurityInfo automatically propagates any
			// inheritable ACE down onto the target's EXISTING descendants
			// (per Microsoft's documented remarks for SetNamedSecurityInfoW),
			// not just ones created afterward. C:\ in particular can have an
			// enormous, slow-to-walk, and largely unrelated existing subtree
			// (Program Files, Users, arbitrary installed software), and
			// stamping a synthetic deny ACE onto all of it would also
			// permanently pollute those machine ACLs and could shadow
			// legitimate workspace Allow entries for repos that happen to
			// live under the system drive. Each of these four paths is
			// listed explicitly (rather than relied on via inheritance from
			// C:\) precisely so a plain, non-inherited Deny placed directly
			// on each one is sufficient: it blocks the denied SIDs from
			// writing (including creating new children) directly under that
			// path without ever touching any descendant's own ACL.
			entries = append(entries, WindowsACLEntry{
				Action:     WindowsACLDenyWrite,
				Path:       denyPath,
				Capability: denySID,
				NoInherit:  true,
				// A non-inherited deny on this root object blocks new writes
				// directly under it, but NOT writes to a pre-existing child
				// that independently grants Users/Authenticated Users write
				// (the access check for that child never evaluates a
				// non-inherited parent ACE). applyWindowsACLPlan therefore
				// enumerates this root's existing writable descendants and
				// applies a direct, non-inheriting deny to each, a bounded,
				// targeted scan that never rewrites the ACL of any descendant
				// that is not itself already writable by those broad groups.
				ScanDescendants: true,
			})
		}

		// Reconcile stale shared/descendant denies: a write-root path here may
		// previously have been covered by the shared-root/descendant DenyWrite
		// mitigation above (either directly, if it IS one of the four shared
		// paths, or as a discovered writable descendant applyWindowsSharedDescendantDenies
		// denied in an earlier run) before the caller configured it as an
		// allowed write root. applyWindowsACLPlan only merges the entries in
		// THIS plan into the existing DACL; it never removes an ACE that is no
		// longer requested, so that old deny would otherwise survive and win
		// over the new Allow under Windows' deny-before-allow evaluation. Every
		// write-root path unconditionally gets a revoke for the stable
		// read-only capability SID: BuildWindowsACLPlan never intentionally
		// places a deny for that SID on a write-root path (see the skip above),
		// so this can never fight an entry this same plan is also adding, and a
		// revoke against a SID with no matching ACE is a safe no-op.
		for _, capability := range writeCapabilities {
			entries = append(entries, WindowsACLEntry{
				Action:     WindowsACLRevokeCapability,
				Path:       capability.Root,
				Capability: denySID,
				NoInherit:  true,
			})
		}
	}

	return WindowsACLPlan{Entries: dedupeWindowsACLEntries(entries)}, nil
}

// windowsPathUnderAnyRoot reports whether path is exactly, or nested under, one
// of the configured write-root capabilities. A shared-path deny must skip both
// cases: denying a root that IS a write root would block the workspace outright,
// and denying a root that merely CONTAINS one (e.g. a shared path of
// C:\Users\Public when C:\Users itself is a configured write root) would place
// an explicit Deny ahead of that root's Allow for every broadened token,
// winning under Windows' deny-before-allow evaluation and jailing a directory
// the user explicitly configured as writable.
func windowsPathUnderAnyRoot(path string, capabilities []windowsWriteRootCapability) bool {
	key := windowsCapabilityPathKey(path)
	if key == "" {
		return false
	}
	for _, cap := range capabilities {
		rootKey := windowsCapabilityPathKey(cap.Root)
		if rootKey == "" {
			continue
		}
		if key == rootKey || strings.HasPrefix(key, rootKey+`\`) {
			return true
		}
	}
	return false
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
