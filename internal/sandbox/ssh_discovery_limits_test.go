package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sshTestBwrapOptions(t *testing.T, profile PermissionProfile) LinuxSandboxBwrapOptions {
	t.Helper()
	dir := t.TempDir()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return LinuxSandboxBwrapOptions{HelperPath: helper, Config: LinuxSandboxHelperConfig{
		PermissionProfile: profile, SandboxPolicyCWD: dir, CommandCWD: dir, Command: []string{"true"},
	}}
}

func assertLinuxCredentialPlanRejected(t *testing.T, profile PermissionProfile) {
	t.Helper()
	_, err := BuildLinuxSandboxBwrapArgs(sshTestBwrapOptions(t, profile))
	if err == nil || !strings.Contains(err.Error(), "mutable symlink") {
		t.Fatalf("unsafe symlink plan did not reject execution: %v", err)
	}
	plan := buildLinuxBwrapFilesystemPlan(profile)
	if plan.Err == nil || len(plan.Args) != 0 {
		t.Fatalf("filesystem planner returned an executable partial plan: %#v", plan)
	}
}

func TestSSHDiscoveryLimitsRejectExecution(t *testing.T) {
	for _, kind := range []string{"directory entry", "config Include match", "config size", "directory depth"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			sshDir := filepath.Join(home, ".ssh")
			switch kind {
			case "directory entry":
				for i := 0; i <= sshPrivateKeyWalkMaxEntries; i++ {
					mustWriteFile(t, filepath.Join(sshDir, fmt.Sprintf("file-%03d", i)), "public data")
				}
			case "config Include match":
				mustWriteFile(t, filepath.Join(sshDir, "config"), "Include includes/*\n")
				for i := 0; i <= sshIncludeMatchCap; i++ {
					mustWriteFile(t, filepath.Join(sshDir, "includes", fmt.Sprintf("%03d", i)), "IdentityFile ~/relocated-key\n")
				}
			case "config size":
				mustWriteFile(t, filepath.Join(sshDir, "config"), strings.Repeat("#", sshConfigMaxBytes+1))
			case "directory depth":
				dir := sshDir
				for i := 0; i <= sshPrivateKeyWalkMaxDepth; i++ {
					dir = filepath.Join(dir, "nested")
				}
				mustWriteFile(t, filepath.Join(dir, "private"), sshPrivateKeyFixture())
			}
			credentials := credentialDenyReadPathsIn(credentialPathOptions{Homes: []string{home}}, nil)
			profile := PermissionProfile{FileSystem: FileSystemPolicy{
				Kind:                      FileSystemRestricted,
				CredentialDiscoveryErrors: credentials.DiscoveryErrors,
			}}
			_, err := BuildLinuxSandboxBwrapArgs(sshTestBwrapOptions(t, profile))
			if err == nil || !strings.Contains(err.Error(), kind+" limit exceeded") {
				t.Fatalf("incomplete discovery did not reject execution: %v", err)
			}
		})
	}
}

func TestLinuxExplicitAbsentDenyRejectsExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future-secret")
	profile := PermissionProfile{FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, DenyRead: []string{path}}}
	_, err := BuildLinuxSandboxBwrapArgs(sshTestBwrapOptions(t, profile))
	if err == nil || !strings.Contains(err.Error(), "cannot guarantee an explicit deny") {
		t.Fatalf("absent explicit deny did not reject execution: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("planning mutated the denied path: %v", err)
	}
}

func TestLinuxSelectiveSSHProtectionRejectsExecution(t *testing.T) {
	home := t.TempDir()
	key := filepath.Join(home, ".ssh", "custom-key")
	mustWriteFile(t, key, sshPrivateKeyFixture())
	credentials := credentialDenyReadPathsIn(credentialPathOptions{Homes: []string{home}}, nil)
	profile := PermissionProfile{FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, SSHDenyReadFiles: credentials.SSHFiles}}
	if err := validateLinuxBwrapPermissionProfile(profile); err == nil || !strings.Contains(err.Error(), "selective SSH key protection") {
		t.Fatalf("selective key mask was accepted: %v", err)
	}
	credentials = finalizeCredentialDenyPaths(credentials, []string{normalizeProfilePath(filepath.Dir(key))})
	if len(credentials.SSHFiles) != 0 {
		t.Fatalf("whole-directory deny did not cover SSH files: %v", credentials.SSHFiles)
	}
}

func TestSSHAllowReadDirectoryKeepsExternalKeyDeny(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	key := filepath.Join(home, "keys", "work")
	mustWriteFile(t, key, sshPrivateKeyFixture())
	mustWriteFile(t, filepath.Join(sshDir, "config"), "IdentityFile ~/keys/work\n")
	credentials := credentialDenyReadPathsIn(credentialPathOptions{Homes: []string{home}}, []string{sshDir})
	if !denyCovered(credentials.Paths, key) {
		t.Fatal("allowing the SSH directory also exposed a referenced key outside it")
	}
}
