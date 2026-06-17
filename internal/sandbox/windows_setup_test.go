package sandbox

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAndParseWindowsSandboxSetupArgs(t *testing.T) {
	home := t.TempDir()
	args, err := BuildWindowsSandboxSetupArgs(WindowsSandboxSetupArgsOptions{
		SandboxHome:    home,
		CommandCWD:     `C:\workspace\src`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind: FileSystemRestricted,
				WriteRoots: []WritableRoot{
					{Root: `C:\workspace`, ProtectedMetadataNames: []string{".git"}},
				},
				DenyRead: []string{`C:\workspace\secret`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsSandboxSetupArgs: %v", err)
	}
	config, err := ParseWindowsSandboxSetupArgs(args)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxSetupArgs: %v", err)
	}
	if config.SandboxHome != home || config.CommandCWD != `C:\workspace\src` || len(config.WorkspaceRoots) != 1 || config.WorkspaceRoots[0] != `C:\workspace` {
		t.Fatalf("setup config = %#v, want sandbox home, command cwd, and workspace root", config)
	}
	if config.PermissionProfile.FileSystem.Kind != FileSystemRestricted || len(config.PermissionProfile.FileSystem.DenyRead) != 1 {
		t.Fatalf("permission profile = %#v, want restricted deny-read profile", config.PermissionProfile)
	}
}

func TestWindowsSandboxSetupPathForRunner(t *testing.T) {
	got := WindowsSandboxSetupPathForRunner(filepath.Join("C:", "zero", WindowsSandboxCommandRunnerName))
	want := filepath.Join("C:", "zero", WindowsSandboxSetupName)
	if got != want {
		t.Fatalf("WindowsSandboxSetupPathForRunner = %q, want %q", got, want)
	}
	if got := WindowsSandboxSetupPathForRunner(""); got != "" {
		t.Fatalf("empty runner setup path = %q, want empty", got)
	}
}

func TestRunWindowsSandboxSetupRejectsInvalidArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := RunWindowsSandboxSetup([]string{"--command-cwd", `C:\workspace`}, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want usage error", code)
	}
	if !strings.Contains(stderr.String(), WindowsSandboxSetupName) {
		t.Fatalf("stderr = %q, want setup helper name", stderr.String())
	}
}

func TestWindowsSandboxSetupMarkerRefreshesWhenProfileChanges(t *testing.T) {
	config := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
				DenyRead:   []string{`C:\workspace\secret-read`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}
	if _, err := WriteWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
	}
	if err := ValidateWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("ValidateWindowsSandboxSetupMarker unchanged: %v", err)
	}

	changed := config
	changed.PermissionProfile.FileSystem.DenyRead = []string{`C:\workspace\other-secret`}
	err := ValidateWindowsSandboxSetupMarker(changed)
	if err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("ValidateWindowsSandboxSetupMarker changed error = %v, want out of date", err)
	}
}

func TestWindowsSandboxSetupMarkerRefreshesWhenNetworkChanges(t *testing.T) {
	config := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
			},
			Network: NetworkPolicy{Mode: NetworkAllow},
		},
	}
	if _, err := WriteWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
	}
	if err := ValidateWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("ValidateWindowsSandboxSetupMarker unchanged: %v", err)
	}

	changed := config
	changed.PermissionProfile.Network = NetworkPolicy{Mode: NetworkDeny}
	err := ValidateWindowsSandboxSetupMarker(changed)
	if err == nil || !strings.Contains(err.Error(), "network policy changed") {
		t.Fatalf("ValidateWindowsSandboxSetupMarker changed error = %v, want network policy changed", err)
	}
}

func TestWindowsSandboxSetupConfigFromCommandPreservesProfileInputs(t *testing.T) {
	command := WindowsSandboxCommandConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace\src`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
				DenyRead:   []string{`C:\workspace\secret`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
		Env:     map[string]string{"ZERO_SANDBOXED": "1"},
		Command: []string{"cmd.exe", "/c", "dir"},
	}
	setup := WindowsSandboxSetupConfigFromCommand(command)
	if setup.SandboxHome != command.SandboxHome || setup.CommandCWD != command.CommandCWD || len(setup.WorkspaceRoots) != 1 || setup.WorkspaceRoots[0] != `C:\workspace` {
		t.Fatalf("setup config = %#v, want command roots", setup)
	}
	if setup.PermissionProfile.FileSystem.Kind != FileSystemRestricted || len(setup.PermissionProfile.FileSystem.DenyRead) != 1 {
		t.Fatalf("setup profile = %#v, want command permission profile", setup.PermissionProfile)
	}
}

func TestWindowsACLPlanHashIsStableAcrossEntryOrder(t *testing.T) {
	left, err := WindowsACLPlanHash(WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLDenyRead, Path: `C:\workspace\secret`, Capability: "S-1-5-21-3", Materialize: true},
		{Action: WindowsACLAllowWrite, Path: `C:\workspace`, Capability: "S-1-5-21-1"},
		{Action: WindowsACLDenyWrite, Path: `C:\workspace\.git`, Capability: "S-1-5-21-1"},
	}})
	if err != nil {
		t.Fatalf("WindowsACLPlanHash left: %v", err)
	}
	right, err := WindowsACLPlanHash(WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLAllowWrite, Path: `c:/workspace`, Capability: "s-1-5-21-1"},
		{Action: WindowsACLDenyWrite, Path: `c:/workspace/.git`, Capability: "S-1-5-21-1"},
		{Action: WindowsACLDenyRead, Path: `c:/workspace/secret`, Capability: "S-1-5-21-3", Materialize: true},
	}})
	if err != nil {
		t.Fatalf("WindowsACLPlanHash right: %v", err)
	}
	if left != right {
		t.Fatalf("ACL plan hashes differ: %q vs %q", left, right)
	}
}
