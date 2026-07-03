package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func testWindowsUnelevatedCommandConfig(t *testing.T) WindowsSandboxCommandConfig {
	t.Helper()
	root := t.TempDir()
	return WindowsSandboxCommandConfig{
		SandboxHome:    filepath.Join(root, ".zero-sandbox"),
		CommandCWD:     root,
		WorkspaceRoots: []string{root},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				ReadRoots:  []string{root},
				WriteRoots: []WritableRoot{{Root: root, ProtectedMetadataNames: []string{".git", ".zero", ".agents"}}},
				AllowTemp:  true,
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
		SandboxLevel: WindowsSandboxLevelUnelevated,
		Command:      []string{"cmd.exe", "/c", "echo hi"},
	}
}

func TestWindowsSandboxCommandArgsRoundTripUnelevatedLevel(t *testing.T) {
	config := testWindowsUnelevatedCommandConfig(t)
	args, err := BuildWindowsSandboxCommandArgs(WindowsSandboxCommandArgsOptions{
		SandboxHome:       config.SandboxHome,
		CommandCWD:        config.CommandCWD,
		WorkspaceRoots:    config.WorkspaceRoots,
		PermissionProfile: config.PermissionProfile,
		SandboxLevel:      WindowsSandboxLevelUnelevated,
		Command:           config.Command,
	})
	if err != nil {
		t.Fatalf("BuildWindowsSandboxCommandArgs: %v", err)
	}
	parsed, err := ParseWindowsSandboxCommandArgs(args)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxCommandArgs: %v", err)
	}
	if parsed.SandboxLevel != WindowsSandboxLevelUnelevated {
		t.Fatalf("round-tripped sandbox level = %q, want %q", parsed.SandboxLevel, WindowsSandboxLevelUnelevated)
	}
}

func TestBuildWindowsUnelevatedAppliedPlanFingerprint(t *testing.T) {
	config := testWindowsUnelevatedCommandConfig(t)
	applied, plan, err := buildWindowsUnelevatedAppliedPlan(config)
	if err != nil {
		t.Fatalf("buildWindowsUnelevatedAppliedPlan: %v", err)
	}
	if applied.ACLPlanHash == "" || applied.ACLPlanEntries == 0 || applied.ACLPlanEntries != len(plan.Entries) {
		t.Fatalf("applied plan = %+v with %d plan entries, want a non-empty consistent fingerprint", applied, len(plan.Entries))
	}
	// Same config -> same fingerprint (the marker's dedupe key).
	again, _, err := buildWindowsUnelevatedAppliedPlan(config)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if again != applied {
		t.Fatalf("fingerprint not stable: %+v then %+v", applied, again)
	}
	// An unrestricted profile has no ACL plan to apply and must error.
	config.PermissionProfile.FileSystem.Kind = FileSystemUnrestricted
	if _, _, err := buildWindowsUnelevatedAppliedPlan(config); err == nil {
		t.Fatal("expected an error for an unrestricted filesystem profile")
	}
}

func TestWindowsUnelevatedSetupMarkerRecordAndLoad(t *testing.T) {
	config := testWindowsUnelevatedCommandConfig(t)
	applied, _, err := buildWindowsUnelevatedAppliedPlan(config)
	if err != nil {
		t.Fatalf("buildWindowsUnelevatedAppliedPlan: %v", err)
	}

	// Missing marker file is the fresh-install state: empty, no error, no match.
	marker, err := loadWindowsUnelevatedSetupMarker(config.SandboxHome)
	if err != nil {
		t.Fatalf("load missing marker: %v", err)
	}
	if marker.contains(applied) {
		t.Fatal("empty marker must not contain the plan")
	}

	if err := recordWindowsUnelevatedAppliedPlan(config.SandboxHome, applied); err != nil {
		t.Fatalf("record: %v", err)
	}
	marker, err = loadWindowsUnelevatedSetupMarker(config.SandboxHome)
	if err != nil {
		t.Fatalf("load recorded marker: %v", err)
	}
	if !marker.contains(applied) || len(marker.AppliedPlans) != 1 {
		t.Fatalf("marker = %+v, want exactly the recorded plan", marker)
	}

	// Recording the same plan again must not duplicate it.
	if err := recordWindowsUnelevatedAppliedPlan(config.SandboxHome, applied); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	marker, err = loadWindowsUnelevatedSetupMarker(config.SandboxHome)
	if err != nil {
		t.Fatalf("reload marker: %v", err)
	}
	if len(marker.AppliedPlans) != 1 {
		t.Fatalf("marker has %d plans after duplicate record, want 1", len(marker.AppliedPlans))
	}
}

func TestWindowsUnelevatedSetupMarkerEvictsOldestPastCap(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".zero-sandbox")
	first := WindowsUnelevatedAppliedPlan{ACLPlanHash: "hash-0", ACLPlanEntries: 1}
	if err := recordWindowsUnelevatedAppliedPlan(home, first); err != nil {
		t.Fatalf("record first: %v", err)
	}
	for index := 1; index <= windowsUnelevatedSetupMarkerMaxPlans; index++ {
		plan := WindowsUnelevatedAppliedPlan{ACLPlanHash: fmt.Sprintf("hash-%d", index), ACLPlanEntries: index}
		if err := recordWindowsUnelevatedAppliedPlan(home, plan); err != nil {
			t.Fatalf("record %d: %v", index, err)
		}
	}
	marker, err := loadWindowsUnelevatedSetupMarker(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(marker.AppliedPlans) != windowsUnelevatedSetupMarkerMaxPlans {
		t.Fatalf("marker has %d plans, want the cap of %d", len(marker.AppliedPlans), windowsUnelevatedSetupMarkerMaxPlans)
	}
	if marker.contains(first) {
		t.Fatal("oldest plan should have been evicted past the cap")
	}
}

func TestLoadWindowsUnelevatedSetupMarkerResetsUnknownSchema(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".zero-sandbox")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir sandbox home: %v", err)
	}
	path := WindowsUnelevatedSetupMarkerPath(home)
	if err := os.WriteFile(path, []byte(`{"schemaVersion":99,"appliedPlans":[{"aclPlanHash":"h","aclPlanEntries":1}]}`), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	marker, err := loadWindowsUnelevatedSetupMarker(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(marker.AppliedPlans) != 0 || marker.SchemaVersion != windowsUnelevatedSetupMarkerSchemaVersion {
		t.Fatalf("unknown schema must reset to empty current-schema marker, got %+v", marker)
	}
}
