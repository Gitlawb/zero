//go:build windows

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// A PLAN HASH ATTESTS A PATHNAME, NOT THE DIRECTORY THAT ANSWERS TO IT.
//
// The unelevated marker records the plan hash and its entry count, both derived
// from pathnames and entries. The runtime root is deterministic and disposable,
// so cleanup can remove the tree and the next command's parent recreates the
// same pathname with ordinary inherited permissions. The hash is unchanged, the
// marker still claims the plan was applied, and the replacement never received
// the capability ACE, leaving the WRITE_RESTRICTED child unable to write TMP or
// its caches with nothing failing to say why.
func TestPlanAttestationFailsAfterTheRootIsRecreated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// Guests: a well-known SID that no ordinary object carries, standing in for
	// the sandbox capability SID.
	const capability = "S-1-5-32-546"
	plan := WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLAllowWrite, Path: root, Capability: capability},
	}}

	if windowsACLPlanStillApplied(plan) {
		t.Fatal("SETUP INVALID: the grant is reported as present before it was applied")
	}
	rollback, err := applyWindowsACLPlan(plan)
	if err != nil {
		t.Fatalf("applyWindowsACLPlan: %v", err)
	}
	t.Cleanup(func() { _ = rollback() })

	if !windowsACLPlanStillApplied(plan) {
		t.Fatal("the grant was just applied and the attestation does not see it")
	}

	// Exactly what cleanup plus the next command's parent does: same pathname,
	// new directory object, ordinary inherited permissions.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if windowsACLPlanStillApplied(plan) {
		t.Error("a recreated root is reported as still carrying the grant, so the apply would be skipped")
	}
}

// A missing path is not a grant either, and must not be read as one.
func TestPlanAttestationFailsWhenThePathIsGone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	plan := WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLAllowWrite, Path: root, Capability: "S-1-5-32-546"},
	}}
	if windowsACLPlanStillApplied(plan) {
		t.Error("a path that does not exist was reported as carrying its grant")
	}
}

// Deny entries are not load-bearing for the child's ability to run, so their
// absence must not force a re-apply on every command.
func TestPlanAttestationIgnoresDenyEntries(t *testing.T) {
	root := t.TempDir()
	plan := WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLDenyWrite, Path: root, Capability: "S-1-5-32-546"},
	}}
	if !windowsACLPlanStillApplied(plan) {
		t.Error("a plan of deny entries alone reported as unapplied, which would re-apply on every command")
	}
}
