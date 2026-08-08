package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// isWindowsVolumeRoot reports whether a cleaned path is the top of a volume,
// with nothing above it: `C:\`, a bare separator, or a UNC share root.
//
// Detected structurally rather than by pattern matching drive letters, because
// filepath.Dir of a root is that same root and of anything else is strictly
// shorter. That holds for drive-qualified paths, for the separator alone, and
// for UNC roots, on either build host.
func isWindowsVolumeRoot(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return false
	}
	return filepath.Dir(cleaned) == cleaned
}

// validateWindowsACLComponent rejects anything that is not a single path
// component.
//
// This is load-bearing in two places, which is why it lives in the portable file
// rather than beside either of them.
//
// At apply time NtCreateFile happily resolves a RELATIVE name containing
// separators, and it resolves it the ordinary way, so an intermediate junction
// inside that name is followed and the object lands outside the pinned parent.
// A name with a separator reopens exactly the hole the parent handle exists to
// close.
//
// At plan time the same shape escapes the write root: a name is joined onto the
// root to place a deny ACE, so ".." or a separator puts that ACE on a directory
// outside the workspace entirely.
//
// The separators are checked explicitly rather than via filepath.Base, because
// these are Windows paths whatever the build host is, and on Linux
// filepath.Base leaves a backslash-joined name untouched and would wave it
// through. A colon is rejected too: it names an alternate data stream or a
// drive, neither of which is a child.
func validateWindowsACLComponent(name string) error {
	switch {
	case name == "":
		return errors.New("windows ACL path component is empty")
	case name == "." || name == "..":
		return fmt.Errorf("windows ACL path component %q is a relative reference, not a child", name)
	case strings.ContainsAny(name, `\/`):
		return fmt.Errorf("windows ACL path component %q contains a separator, so it would resolve through intermediate directories instead of staying a child", name)
	case strings.Contains(name, ":"):
		return fmt.Errorf("windows ACL path component %q contains a colon, which names a stream or a drive rather than a child", name)
	}
	return nil
}

type WindowsACLAction string

const (
	WindowsACLAllowWrite WindowsACLAction = "allow-write"
	WindowsACLDenyRead   WindowsACLAction = "deny-read"
	WindowsACLDenyWrite  WindowsACLAction = "deny-write"
	// WindowsACLDenyDelete denies removing or renaming the object it names,
	// WITHOUT denying writes to it or inside it, and without inheriting.
	//
	// It exists for .git. The write-denied carveouts live on .git/config and
	// .git/hooks as objects, so replacing the .git directory discards them: the
	// recreated config and hooks inherit the workspace allow with no deny, which
	// restores credential.helper and core.hooksPath. .git cannot simply join
	// sandboxFullyProtectedMetadataNames, because DenyWrite's mask includes
	// FILE_GENERIC_WRITE and git must write index, objects and refs.
	WindowsACLDenyDelete WindowsACLAction = "deny-delete"
)

type WindowsACLEntry struct {
	Action      WindowsACLAction `json:"action"`
	Path        string           `json:"path"`
	Capability  string           `json:"capability"`
	Materialize bool             `json:"materialize,omitempty"`
	// MaterializeFile makes Materialize create an empty FILE instead of a
	// directory. Only meaningful with Materialize. .git/config is the case that
	// forces the distinction: created as a directory it does not merely carry
	// the wrong ACL, it makes `git init` fail outright.
	MaterializeFile bool `json:"materializeFile,omitempty"`
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
		key := string(entry.Action) + "\x00" + windowsCapabilityPathKey(entry.Path) + "\x00" + strings.ToLower(entry.Capability)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, entry)
	}
	return out
}
