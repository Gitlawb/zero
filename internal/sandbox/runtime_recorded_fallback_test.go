package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// A RECORDED ROOT IS HISTORY. IT MUST NOT BE RE-DERIVED FROM TODAY'S TEMP.
//
// Setup reaches the temp fallback when the preferred cache-derived root cannot
// be leased, and it records that concrete root in the marker. Command selection
// then recognised the record only by deriving today's fallback from this
// process's TEMP and comparing. A later IDE, service or terminal running with a
// different TEMP therefore matched neither of today's candidates: the record was
// ignored, selection produced a tree setup never provisioned, and the runner
// rejected the marker as out of date. Re-running setup from the original
// environment repeats the original answer and does not make the other
// environment converge.
//
// The record is now recognised by its own shape instead, which is the question
// that was actually being asked: does this belong to this workspace and home.
//
// Not gated on Windows. The marker only exists where setup wrote one, so this is
// Windows-only in practice, but keeping it platform-neutral means the
// setup-to-command contract runs on every CI runner rather than only one.
func TestARecordedFallbackSurvivesATempChange(t *testing.T) {
	home := t.TempDir()
	workspace := canonicalSandboxWorkspaceRoot(t.TempDir())

	tempA := t.TempDir()
	t.Setenv("TMP", tempA)
	t.Setenv("TEMP", tempA)
	recorded, err := fallbackSandboxRuntimeRoot(workspace)
	if err != nil {
		t.Skipf("no fallback runtime root available here: %v", err)
	}
	if err := os.MkdirAll(recorded, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRecordedRoot(t, home, recorded)

	// SETUP: with the original TEMP the record is honoured, so a failure after the
	// flip is about the flip and not about the record being unusable.
	preferred := preferredRuntimeRootFor(t, workspace)
	if got := pinnedSandboxRuntimeRoot(workspace, preferred, recorded, home); got != recorded {
		t.Fatalf("SETUP INVALID: the record is not honoured even before the temp change: got %q, want %q", got, recorded)
	}

	// The same command, run later from an environment with a different TEMP.
	tempB := t.TempDir()
	t.Setenv("TMP", tempB)
	t.Setenv("TEMP", tempB)
	derivedNow, err := fallbackSandboxRuntimeRoot(workspace)
	if err != nil {
		t.Skipf("no fallback runtime root under the second temp: %v", err)
	}
	if sameWindowsRuntimeRootPath(derivedNow, recorded) {
		t.Skip("SETUP: the temp change did not move the derived fallback, so there is nothing to diverge")
	}

	if got := pinnedSandboxRuntimeRoot(workspace, preferred, derivedNow, home); got != recorded {
		t.Fatalf("selection abandoned the root setup provisioned after only TEMP changed: got %q, want the recorded %q", got, recorded)
	}
}

// preferredRuntimeRootFor is the cache-derived candidate, which must stay a
// candidate: this change is about the fallback only.
func preferredRuntimeRootFor(t *testing.T, workspace string) string {
	t.Helper()
	cacheRoot, err := sandboxUserCacheDir()
	if err != nil {
		t.Skipf("no user cache directory here: %v", err)
	}
	root, err := sandboxRuntimeRootFor(workspace, canonicalSandboxWorkspaceRoot(cacheRoot))
	if err != nil {
		t.Skipf("no preferred runtime root here: %v", err)
	}
	return root
}

// AND THE PROTECTION THAT MADE THE OLD COMPARISON WORTH HAVING STAYS.
//
// Recognising the record by shape must not turn into recognising anybody's
// record. A fallback root belonging to a DIFFERENT workspace has the same owned
// shape and a different digest, so it is still refused.
func TestARecordedFallbackForAnotherWorkspaceIsStillRefused(t *testing.T) {
	home := t.TempDir()
	mine := canonicalSandboxWorkspaceRoot(t.TempDir())
	theirs := canonicalSandboxWorkspaceRoot(t.TempDir())

	temp := t.TempDir()
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)

	theirRoot, err := fallbackSandboxRuntimeRoot(theirs)
	if err != nil {
		t.Skipf("no fallback runtime root available here: %v", err)
	}
	writeRecordedRoot(t, home, theirRoot)

	if got := pinnedSandboxRuntimeRoot(mine, filepath.Join(t.TempDir(), "preferred"), filepath.Join(t.TempDir(), "fallback"), home); got != "" {
		t.Fatalf("pinned %q, which was provisioned for another workspace", got)
	}
}

// A record with no owned runtime shape at all is refused too, so the shape test
// is doing work rather than waving anything through.
func TestARecordedRootWithoutTheOwnedShapeIsRefused(t *testing.T) {
	home := t.TempDir()
	workspace := canonicalSandboxWorkspaceRoot(t.TempDir())
	writeRecordedRoot(t, home, filepath.Join(t.TempDir(), "not-a-runtime-root"))

	if got := pinnedSandboxRuntimeRoot(workspace, filepath.Join(t.TempDir(), "preferred"), filepath.Join(t.TempDir(), "fallback"), home); got != "" {
		t.Fatalf("pinned %q, which has none of the owned runtime shape", got)
	}
}
