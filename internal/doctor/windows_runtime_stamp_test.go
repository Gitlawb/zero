package doctor

import (
	"os"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sandbox"
)

// DOCTOR HAS TO ASK THE ONE QUESTION THE MARKER CANNOT ANSWER.
//
// The marker hashes ACL-plan entries, which are pathnames. Whether the directory
// those pathnames resolve to is still the tree setup provisioned is a different
// question, and the runtime stamp is what answers it. validateWindowsSandboxRuntimeStamp
// returns nil early when profile.Runtime is nil, which is correct for the setup
// side and for unrestricted profiles, and was wrong here: doctor built its
// profile with PermissionProfileFromPolicy, which never sets Runtime, so the
// check was skipped and `zero doctor` reported a healthy sandbox on exactly the
// state the stamp was added to detect -- an evicted runtime tree, silently
// recreated with ordinary permissions and no capability ACE, where every
// sandboxed command then fails with nothing explaining why.
// doctorRuntimeCandidate returns a runtime root this workspace would really
// select, taken from the candidate set the Windows plan folds in.
//
// It has to be a REAL candidate. An arbitrary path is already in the ACL plan on
// one side and not the other, so the plan hashes diverge and validation fails
// before the stamp is ever consulted -- which would make this test fail without
// the fix for a reason that has nothing to do with the stamp.
func doctorRuntimeCandidate(t *testing.T, workspace string) string {
	t.Helper()
	bare := doctorProfile(t, workspace)
	augmented := sandbox.WindowsSandboxProfileWithRuntimeRoots(bare, []string{workspace})
	existing := map[string]bool{}
	for _, root := range bare.FileSystem.WriteRoots {
		existing[root.Root] = true
	}
	for _, root := range augmented.FileSystem.WriteRoots {
		if !existing[root.Root] {
			return root.Root
		}
	}
	t.Skip("no runtime candidate is derivable in this environment")
	return ""
}

func doctorProfile(t *testing.T, workspace string) sandbox.PermissionProfile {
	t.Helper()
	scope, err := sandbox.NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return sandbox.PermissionProfileFromPolicy(workspace, doctorSandboxPolicy(config.SandboxConfig{}), scope)
}

func writeDoctorSetupMarker(t *testing.T, home, workspace, runtimeRoot string) {
	t.Helper()
	profile := doctorProfile(t, workspace)
	setup := sandbox.WindowsSandboxSetupConfig{
		SandboxHome:    home,
		CommandCWD:     workspace,
		WorkspaceRoots: []string{workspace},
		PermissionProfile: sandbox.WindowsSandboxProfileWithRuntimeRoots(
			sandbox.PermissionProfileWithRuntimeRoot(profile, runtimeRoot),
			[]string{workspace},
		),
	}
	if _, err := sandbox.WriteWindowsSandboxSetupMarker(setup); err != nil {
		t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
	}
}

func TestDoctorReportsAnEvictedRuntimeTree(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("ZERO_WINDOWS_SANDBOX_HOME", home)

	runtimeRoot := doctorRuntimeCandidate(t, workspace)
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		t.Fatalf("create the runtime root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	writeDoctorSetupMarker(t, home, workspace, runtimeRoot)

	backend := sandbox.Backend{Name: sandbox.BackendWindowsRestrictedToken}

	// A healthy machine first, or the eviction assertion below would be satisfied
	// by a check that warns unconditionally.
	if result := windowsSandboxSetupCheck("windows", backend, workspace, config.SandboxConfig{}); result != nil {
		t.Fatalf("a freshly set-up machine was reported unhealthy: %s", result.Message)
	}

	// cleanupSandboxRuntimeRoots evicts inactive roots on an age and count policy,
	// and the next run recreates the pathname with inherited permissions. The plan
	// hash never moves, so only the stamp can tell.
	if err := os.RemoveAll(runtimeRoot); err != nil {
		t.Fatalf("evict the runtime root: %v", err)
	}
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		t.Fatalf("recreate the pathname the way an ordinary run would: %v", err)
	}

	result := windowsSandboxSetupCheck("windows", backend, workspace, config.SandboxConfig{})
	if result == nil {
		t.Fatal("doctor reported a healthy sandbox while the provisioned runtime tree was gone; every sandboxed command on this machine would fail with nothing explaining why")
	}
	if !strings.Contains(strings.ToLower(result.Message), "setup") {
		t.Errorf("the warning does not point at setup: %s", result.Message)
	}
}
