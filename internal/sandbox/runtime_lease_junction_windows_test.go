//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// leaseRootUnder builds a production-shaped runtime root beneath a test-owned
// cache directory, so the owned tail is real rather than assumed.
func leaseRootUnder(t *testing.T, cacheRoot string) string {
	t.Helper()
	workspace := t.TempDir()
	root, ok := deterministicSandboxRuntimeRoot(canonicalSandboxWorkspaceRoot(workspace), canonicalSandboxWorkspaceRoot(cacheRoot))
	if !ok {
		t.Fatalf("SETUP INVALID: no deterministic runtime root under %s", cacheRoot)
	}
	return root
}

// THE LEASE IS THE FIRST WRITE SETUP MAKES, SO IT IS THE ONE THAT MATTERS MOST.
//
// prepareSandboxRuntimeLease checked the owned components for aliases and then
// created the parent with os.MkdirAll and opened "<root>.lease" by pathname.
// Both follow. An ordinary same-account process could replace "zero", "runtime"
// or "v1" with a junction after the check and before either call, and elevated
// setup would then build the tree and the lease file inside somebody else's
// target. Putting the component back afterwards left the later handle-relative
// provisioning working on the legitimate tree, so no post-check ever saw it, and
// the rollback could not compensate what it had no record of.
//
// A junction needs no privilege on Windows, so the caller's half of this races
// on an ordinary unelevated box. Here the junction is simply left in place,
// which is the strictly easier case to detect: if the redirected target receives
// anything at all, the pathname walk is still live.
func TestRuntimeLeaseRefusesAJunctionedOwnedComponent(t *testing.T) {
	cacheRoot := t.TempDir()
	target := t.TempDir()
	root := leaseRootUnder(t, cacheRoot)

	owned := filepath.Join(canonicalSandboxWorkspaceRoot(cacheRoot), "zero")
	if !strings.HasPrefix(strings.ToLower(root), strings.ToLower(owned)) {
		t.Fatalf("SETUP INVALID: %s does not sit under the owned component %s", root, owned)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", owned, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable here: %v\n%s", err, out)
	}

	// SETUP: the junction really redirects, or a clean target below proves nothing.
	probe := filepath.Join(owned, "probe.txt")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		t.Skipf("the junction does not accept writes here: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "probe.txt")); err != nil {
		t.Skipf("SETUP: the junction does not redirect on this filesystem: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}

	lease, _, err := prepareSandboxRuntimeLeaseRecording(root)
	if lease != nil {
		lease.release()
	}
	if err == nil {
		t.Fatal("lease acquisition descended through a junction the caller planted")
	}

	entries, readErr := os.ReadDir(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("lease acquisition created %v beneath the redirected target", names)
	}
}

// And an ordinary cache tree still gets its lease, or the refusal above would be
// satisfied by an acquirer that refuses everything.
func TestRuntimeLeaseStillAcquiresOnAnOrdinaryTree(t *testing.T) {
	cacheRoot := t.TempDir()
	root := leaseRootUnder(t, cacheRoot)

	lease, created, err := prepareSandboxRuntimeLeaseRecording(root)
	if err != nil {
		t.Fatalf("an ordinary cache tree was refused: %v", err)
	}
	defer lease.release()

	if _, statErr := os.Stat(sandboxRuntimeLeasePath(root)); statErr != nil {
		t.Fatalf("the lease file was not created: %v", statErr)
	}
	// The components above the leaf are recorded, so a later failure can undo
	// them. The leaf itself belongs to provisioning and must NOT appear here.
	for _, entry := range created {
		if strings.EqualFold(filepath.Clean(entry.path), filepath.Clean(root)) {
			t.Fatalf("lease acquisition claimed the runtime leaf %s, which provisioning owns", root)
		}
	}
	if len(created) == 0 {
		t.Fatal("SETUP INVALID: nothing was recorded as created, so the accounting assertion above is vacuous")
	}
}

// A second acquisition over an existing tree records nothing: it created nothing,
// so it owns nothing, and rollback must not remove a tree it found.
func TestRuntimeLeaseRecordsOnlyWhatItCreated(t *testing.T) {
	cacheRoot := t.TempDir()
	root := leaseRootUnder(t, cacheRoot)

	first, _, err := prepareSandboxRuntimeLeaseRecording(root)
	if err != nil {
		t.Fatalf("first acquisition: %v", err)
	}
	first.release()

	second, created, err := prepareSandboxRuntimeLeaseRecording(root)
	if err != nil {
		t.Fatalf("second acquisition: %v", err)
	}
	defer second.release()
	if len(created) != 0 {
		t.Fatalf("the second acquisition claimed %d directories it did not create; rollback would delete a tree it found", len(created))
	}
}
