//go:build windows

package sandbox

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsACLPlanStillApplied reports whether the objects a plan names still
// carry the grants it describes.
//
// THE MARKER FINGERPRINTS A PLAN, NOT AN OBJECT. Its hash is computed from
// pathnames and entries, so it records what SHOULD be granted and can never
// establish that whatever directory currently answers to that name has received
// it. The runtime root makes the difference reachable rather than theoretical:
// it is deterministic and disposable, so cleanup removes the tree, the next
// command's parent recreates the same pathname with ordinary inherited
// permissions, and the plan hash is unchanged. The fast path then skipped the
// apply entirely and the WRITE_RESTRICTED child could not write TMP or its
// language and package caches, with nothing failing to say why.
//
// Attesting the object costs one security-descriptor read per allow entry, on a
// path that is about to create a process, and it covers every reason a grant
// can be missing rather than only recreation by the parent: manual deletion,
// an eviction between commands, a restored backup.
//
// Failure is treated as "not applied". Reapplying a grant that is already there
// is idempotent and cheap; skipping one that is absent produces a sandbox that
// silently cannot write.
func windowsACLPlanStillApplied(plan WindowsACLPlan) bool {
	for _, entry := range plan.Entries {
		if entry.Action != WindowsACLAllowWrite {
			// Only the allow grants are load-bearing for the child's ability to
			// run. A missing DENY is a weaker boundary rather than a broken one,
			// and re-applying the whole plan is what fixes either.
			continue
		}
		if strings.TrimSpace(entry.Path) == "" || strings.TrimSpace(entry.Capability) == "" {
			continue
		}
		if !windowsPathGrantsTrustee(entry.Path, entry.Capability) {
			return false
		}
	}
	return true
}

// windowsPathGrantsTrustee reports whether path's DACL carries an allow entry
// for the given SID string.
func windowsPathGrantsTrustee(path, trustee string) bool {
	wanted, err := windows.StringToSid(trustee)
	if err != nil {
		return false
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return false
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var header *windows.ACE_HEADER
		if err := windows.GetAce(dacl, index, (**windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(&header))); err != nil {
			return false
		}
		ace := (*windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(header))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(wanted) {
			return true
		}
	}
	return false
}
