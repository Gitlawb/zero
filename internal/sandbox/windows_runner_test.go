package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsCapabilitySIDsPersistAndReuse(t *testing.T) {
	home := t.TempDir()
	caps, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	if caps.SchemaVersion != windowsCapabilitySIDSchemaVersion || caps.ReadOnly == "" {
		t.Fatalf("capability SIDs = %#v, want schema and read-only SID", caps)
	}
	if _, err := os.Stat(WindowsCapabilitySIDPath(home)); err != nil {
		t.Fatalf("capability SID file missing: %v", err)
	}

	again, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs again: %v", err)
	}
	if again.ReadOnly != caps.ReadOnly {
		t.Fatalf("read-only SID changed: first=%q second=%q", caps.ReadOnly, again.ReadOnly)
	}
}

func TestWindowsCapabilitySIDsAreScopedByRoot(t *testing.T) {
	home := t.TempDir()
	workspaceA := `C:\Work\Project`
	workspaceB := `c:/work/project`
	extra := `D:\Cache`

	first, err := WindowsWorkspaceCapabilitySID(home, workspaceA)
	if err != nil {
		t.Fatalf("WindowsWorkspaceCapabilitySID first: %v", err)
	}
	second, err := WindowsWorkspaceCapabilitySID(home, workspaceB)
	if err != nil {
		t.Fatalf("WindowsWorkspaceCapabilitySID second: %v", err)
	}
	if first != second {
		t.Fatalf("equivalent workspace roots got different SIDs: %q vs %q", first, second)
	}

	extraSID, err := WindowsWritableRootCapabilitySID(home, extra)
	if err != nil {
		t.Fatalf("WindowsWritableRootCapabilitySID: %v", err)
	}
	if extraSID == "" || extraSID == first {
		t.Fatalf("writable root SID = %q, workspace SID = %q; want distinct non-empty SIDs", extraSID, first)
	}
}

func TestWindowsCapabilitySIDsForConfigSelectsReadOnlySID(t *testing.T) {
	home := t.TempDir()
	caps, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	got, err := WindowsCapabilitySIDsForConfig(WindowsSandboxCommandConfig{
		SandboxHome: home,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{Kind: FileSystemRestricted},
			Network:    NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("WindowsCapabilitySIDsForConfig: %v", err)
	}
	if len(got) != 1 || got[0] != caps.ReadOnly {
		t.Fatalf("capability SIDs = %#v, want read-only SID %q", got, caps.ReadOnly)
	}
}

func TestWindowsCapabilitySIDsForConfigSelectsWritableRootSIDs(t *testing.T) {
	home := t.TempDir()
	config := WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind: FileSystemRestricted,
				WriteRoots: []WritableRoot{
					{Root: `C:\workspace`},
					{Root: `D:\cache`},
				},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}
	got, err := WindowsCapabilitySIDsForConfig(config)
	if err != nil {
		t.Fatalf("WindowsCapabilitySIDsForConfig: %v", err)
	}
	workspaceSID, err := WindowsWorkspaceCapabilitySID(home, `c:/workspace`)
	if err != nil {
		t.Fatalf("WindowsWorkspaceCapabilitySID: %v", err)
	}
	extraSID, err := WindowsWritableRootCapabilitySID(home, `d:/cache`)
	if err != nil {
		t.Fatalf("WindowsWritableRootCapabilitySID: %v", err)
	}
	want := []string{workspaceSID, extraSID}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("capability SIDs = %#v, want %#v", got, want)
	}
}

func TestResolveWindowsSandboxHomeHonorsOverride(t *testing.T) {
	root := t.TempDir()
	home, err := ResolveWindowsSandboxHome(map[string]string{"ZERO_WINDOWS_SANDBOX_HOME": filepath.Join(root, "sandbox")})
	if err != nil {
		t.Fatalf("ResolveWindowsSandboxHome: %v", err)
	}
	if home != filepath.Join(root, "sandbox") {
		t.Fatalf("sandbox home = %q, want override", home)
	}
}

func TestBuildWindowsSandboxCommandArgsRequiresSandboxHome(t *testing.T) {
	_, err := ParseWindowsSandboxCommandArgs([]string{
		"--command-cwd", `C:\workspace`,
		"--permission-profile", `{"fileSystem":{"kind":"restricted"},"network":{"mode":"deny"}}`,
		"--env-json", `{}`,
		"--windows-sandbox-level", "restricted-token",
		"--workspace-root", `C:\workspace`,
		"--", "cmd.exe",
	})
	if err == nil || !strings.Contains(err.Error(), "--sandbox-home") {
		t.Fatalf("ParseWindowsSandboxCommandArgs error = %v, want missing sandbox home", err)
	}
}

func TestRunWindowsSandboxCommandRunnerRejectsInvalidArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := RunWindowsSandboxCommandRunner([]string{"--command-cwd", `C:\workspace`}, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want usage error", code)
	}
	if !strings.Contains(stderr.String(), WindowsSandboxCommandRunnerName) {
		t.Fatalf("stderr = %q, want runner name", stderr.String())
	}
}
