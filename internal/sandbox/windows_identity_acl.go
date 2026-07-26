package sandbox

// ACLs for a sandbox principal.
//
// The capability-SID model this sits beside starts from "the caller can already
// read everything" and narrows writes, because the sandboxed child runs as the
// caller. A principal inverts that: a separate local account has no access to
// the caller's profile at all, so the interesting direction is what to GRANT.
//
// That inversion is the point. Credential stores under the user's profile are
// unreachable because the principal is a different account, not because a deny
// rule enumerated them, which is what makes this able to close #662 and #675 on
// Windows where a deny-read ACE against the caller's own SID never could. Deny
// rules stay useful only for objects that are readable by everyone.
//
// Grants are explicit and narrow: the workspace and any extra write roots get
// read+write, declared read-only roots get read, and the protected metadata
// carve-outs the profile already defines stay denied so .git internals and
// .zero/.agents cannot be rewritten from inside the sandbox.

import (
	"errors"
	"fmt"
	"path/filepath"
)

// WindowsACLAllowRead grants read and execute without write. It exists for the
// principal model, where a read root must be granted rather than assumed.
const WindowsACLAllowRead WindowsACLAction = "allow-read"

// windowsPrincipalACLInput is everything needed to describe a principal's
// access. It is deliberately a plain struct rather than the full command config
// so the plan can be built and tested without a live sandbox.
type windowsPrincipalACLInput struct {
	// PrincipalSID is the string SID of the sandbox account every ACE names.
	PrincipalSID string
	// WriteRoots receive read+write+execute. The workspace lives here.
	WriteRoots []WritableRoot
	// ReadRoots receive read+execute only.
	ReadRoots []string
	// DenyRead covers objects a principal could otherwise reach because they are
	// world-readable; per-user secrets need no entry.
	DenyRead []string
}

// buildWindowsPrincipalACLPlan turns a principal's access into ACL entries.
//
// Ordering matters at apply time: deny entries are emitted before allows so a
// carve-out inside a granted root survives, which mirrors how Windows evaluates
// an explicit DACL (deny ACEs first).
func buildWindowsPrincipalACLPlan(input windowsPrincipalACLInput) (WindowsACLPlan, error) {
	if input.PrincipalSID == "" {
		return WindowsACLPlan{}, errors.New("windows principal ACL plan requires a principal SID")
	}
	if len(input.WriteRoots) == 0 && len(input.ReadRoots) == 0 {
		return WindowsACLPlan{}, errors.New("windows principal ACL plan requires at least one root")
	}

	entries := make([]WindowsACLEntry, 0, len(input.WriteRoots)*2+len(input.ReadRoots)+len(input.DenyRead))

	// Deny first. A deny ACE inside a write root (protected metadata, git
	// internals) has to win over the grant that follows it.
	for _, path := range normalizeProfilePaths(input.DenyRead) {
		entries = append(entries, WindowsACLEntry{
			Action:     WindowsACLDenyRead,
			Path:       path,
			Capability: input.PrincipalSID,
		})
	}
	for _, root := range input.WriteRoots {
		// Normalized the same way as read and deny paths: a write root may arrive
		// with "~" or as a relative path, and an ACE has to name the same absolute,
		// symlink-resolved object the deny entries do or the two disagree.
		cleaned := normalizeProfilePath(root.Root)
		if cleaned == "" {
			return WindowsACLPlan{}, fmt.Errorf("windows principal ACL plan: unusable write root %q", root.Root)
		}
		for _, subpath := range normalizeProfilePaths(root.ReadOnlySubpaths) {
			entries = append(entries, WindowsACLEntry{
				Action:     WindowsACLDenyWrite,
				Path:       subpath,
				Capability: input.PrincipalSID,
			})
		}
		for _, name := range root.ProtectedMetadataNames {
			entries = append(entries, WindowsACLEntry{
				Action:      WindowsACLDenyWrite,
				Path:        filepath.Join(cleaned, name),
				Capability:  input.PrincipalSID,
				Materialize: true,
			})
		}
	}

	// Then the grants the principal cannot work without.
	for _, root := range input.WriteRoots {
		cleaned := normalizeProfilePath(root.Root)
		if cleaned == "" {
			continue
		}
		entries = append(entries, WindowsACLEntry{
			Action:     WindowsACLAllowWrite,
			Path:       cleaned,
			Capability: input.PrincipalSID,
		})
	}
	for _, path := range normalizeProfilePaths(input.ReadRoots) {
		entries = append(entries, WindowsACLEntry{
			Action:     WindowsACLAllowRead,
			Path:       path,
			Capability: input.PrincipalSID,
		})
	}

	return WindowsACLPlan{Entries: dedupeWindowsACLEntries(entries)}, nil
}

// windowsPrincipalRevokePlan returns the entries whose ACEs should be removed
// when a principal is retired. Revocation is by TRUSTEE rather than by path:
// every ACE naming this principal is dropped, so cleanup does not depend on
// remembering which paths were granted, and a grant added by an older version
// is still removed.
//
// This is the removal path the capability-SID model never had, where a synthetic
// SID left ACEs behind on the user's tree with nothing to match them against.
func windowsPrincipalRevokePlan(principalSID string, paths []string) (WindowsACLPlan, error) {
	if principalSID == "" {
		return WindowsACLPlan{}, errors.New("windows principal revoke plan requires a principal SID")
	}
	cleaned := normalizeProfilePaths(paths)
	entries := make([]WindowsACLEntry, 0, len(cleaned))
	for _, path := range cleaned {
		entries = append(entries, WindowsACLEntry{
			Action:     windowsACLRevoke,
			Path:       path,
			Capability: principalSID,
		})
	}
	return WindowsACLPlan{Entries: entries}, nil
}

// windowsACLRevoke removes every ACE naming the trustee on a path, whatever
// access it granted or denied.
const windowsACLRevoke WindowsACLAction = "revoke"
