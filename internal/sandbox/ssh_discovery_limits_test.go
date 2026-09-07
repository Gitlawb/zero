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
	for _, kind := range []string{"config Include match", "config size"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			sshDir := filepath.Join(home, ".ssh")
			switch kind {
			case "config Include match":
				mustWriteFile(t, filepath.Join(sshDir, "config"), "Include includes/*\n")
				for i := 0; i <= sshIncludeMatchCap; i++ {
					mustWriteFile(t, filepath.Join(sshDir, "includes", fmt.Sprintf("%03d", i)), "IdentityFile ~/relocated-key\n")
				}
			case "config size":
				mustWriteFile(t, filepath.Join(sshDir, "config"), strings.Repeat("#", sshConfigMaxBytes+1))
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

func TestSSHDiscoveryWalksLargeAndDeepDirectories(t *testing.T) {
	for _, kind := range []string{"large", "deep"} {
		t.Run(kind, func(t *testing.T) {
			sshDir := filepath.Join(t.TempDir(), ".ssh")
			dir := sshDir
			if kind == "large" {
				for i := range 600 {
					mustWriteFile(t, filepath.Join(dir, fmt.Sprintf("public-%03d", i)), "public data")
				}
			} else {
				for range 12 {
					dir = filepath.Join(dir, "d")
				}
			}
			key := filepath.Join(dir, "work-key")
			mustWriteFile(t, key, sshPrivateKeyFixture())
			scanner := &sshDiscovery{}
			keys := scanner.walkPrivateKeyFiles(sshDir)
			if len(scanner.errors) != 0 || len(keys) != 1 || keys[0] != key {
				t.Fatalf("discovery = %v, errors = %v; want the nested key", keys, scanner.errors)
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

func TestLinuxAbsentSSHKeyRefusesCommandBeforeCreation(t *testing.T) {
	for _, kind := range []string{"absent-directory", "empty-directory", "configured-external-key"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			sshDir := filepath.Join(home, ".ssh")
			key := filepath.Join(sshDir, "id_ed25519")
			var allowRead []string
			if kind == "empty-directory" {
				if err := os.Mkdir(sshDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "configured-external-key" {
				key = filepath.Join(home, "keys", "future-key")
				mustWriteFile(t, filepath.Join(sshDir, "config"), "IdentityFile ~/keys/future-key\n")
				// Isolate the external candidate from the conventional key denies.
				allowRead = []string{sshDir}
			}
			credentials := credentialDenyReadPathsIn(credentialPathOptions{Homes: []string{home}}, allowRead)
			profile := PermissionProfile{FileSystem: FileSystemPolicy{
				Kind: FileSystemRestricted, ReadRoots: []string{"/"},
				DenyReadIfExists: credentials.Paths, SSHDenyReadFiles: credentials.SSHFiles,
			}}
			// Fail closed before launch: there must be no running sandbox in which
			// a trusted host writer can later make this key readable.
			for _, created := range []bool{false, true} {
				if created {
					mustWriteFile(t, key, sshPrivateKeyFixture())
				}
				args, err := BuildLinuxSandboxBwrapArgs(sshTestBwrapOptions(t, profile))
				if err == nil || !strings.Contains(err.Error(), "selective SSH key protection") || len(args) != 0 {
					t.Errorf("profile constructed before key creation allowed command planning (created=%v): %v", created, err)
				}
			}
			// An explicit directory deny covers both present and future keys.
			credentials = finalizeCredentialDenyPaths(credentials, []string{normalizeProfilePath(filepath.Dir(key))})
			if len(credentials.SSHFiles) != 0 {
				t.Errorf("containing-directory deny did not cover future external key: %v", credentials.SSHFiles)
			}
		})
	}
}
