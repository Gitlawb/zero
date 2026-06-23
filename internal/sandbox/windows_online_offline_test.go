package sandbox

import (
	"encoding/json"
	"os"
	"testing"
)

// windowsRuntimeTokenSIDs is the per-mode restricting-SID composer: both modes
// keep the write-capability SIDs (write-jail), deny additionally carries the
// offline-marker SID the WFP block filter matches.
func TestWindowsRuntimeTokenSIDs(t *testing.T) {
	caps := []string{"S-1-write-a", "S-1-write-b"}
	offline := "S-1-offline"

	deny := windowsRuntimeTokenSIDs(caps, offline, NetworkDeny)
	if !containsString(deny, offline) {
		t.Errorf("deny token must carry the offline-marker SID: %v", deny)
	}
	allow := windowsRuntimeTokenSIDs(caps, offline, NetworkAllow)
	if containsString(allow, offline) {
		t.Errorf("allow token must NOT carry the offline-marker SID: %v", allow)
	}
	// Both modes keep the write-capability SIDs — the workspace write-jail holds
	// either way.
	for _, mode := range []NetworkMode{NetworkDeny, NetworkAllow} {
		got := windowsRuntimeTokenSIDs(caps, offline, mode)
		for _, c := range caps {
			if !containsString(got, c) {
				t.Errorf("mode %q dropped write-capability SID %q: %v", mode, c, got)
			}
		}
	}
}

// The provisioned infrastructure is identical for allow and deny configs, so one
// setup serves both modes (and its fingerprint is stable across modes).
func TestBuildWindowsNetworkInfraPlanIsModeIndependent(t *testing.T) {
	home := t.TempDir()
	mk := func(mode NetworkMode) WindowsSandboxCommandConfig {
		return WindowsSandboxCommandConfig{
			SandboxHome:    home,
			CommandCWD:     `C:\ws`,
			WorkspaceRoots: []string{`C:\ws`},
			PermissionProfile: PermissionProfile{
				FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, WriteRoots: []WritableRoot{{Root: `C:\ws`}}},
				Network:    NetworkPolicy{Mode: mode},
			},
		}
	}
	denyPlan, err := BuildWindowsNetworkInfraPlan(mk(NetworkDeny))
	if err != nil {
		t.Fatalf("deny infra plan: %v", err)
	}
	allowPlan, err := BuildWindowsNetworkInfraPlan(mk(NetworkAllow))
	if err != nil {
		t.Fatalf("allow infra plan: %v", err)
	}
	if len(denyPlan.Filters) != 14 || len(denyPlan.IdentitySIDs) != 1 {
		t.Fatalf("infra plan should be 2 broad plus 12 targeted block filters scoped to 1 offline SID, got %#v", denyPlan)
	}
	denyHash, _ := WindowsNetworkInfraHash(denyPlan)
	allowHash, _ := WindowsNetworkInfraHash(allowPlan)
	if denyHash != allowHash || denyHash == "" {
		t.Fatalf("infra hash must be identical across modes: deny=%q allow=%q", denyHash, allowHash)
	}
	// The plan is scoped to the offline-marker SID, never the write-capability SIDs.
	offline, err := WindowsOfflineMarkerSID(home)
	if err != nil {
		t.Fatalf("offline SID: %v", err)
	}
	if denyPlan.IdentitySIDs[0] != offline {
		t.Errorf("infra plan SID = %q, want offline-marker %q", denyPlan.IdentitySIDs[0], offline)
	}
}

// A pre-existing schema-1 capability file (no Offline SID) is upgraded in place:
// an Offline SID is minted and persisted, idempotently.
func TestLoadOrCreateWindowsCapabilitySIDsUpgradesOffline(t *testing.T) {
	home := t.TempDir()
	legacy := WindowsCapabilitySIDs{SchemaVersion: 1, ReadOnly: "S-1-ro"}
	bytes, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(WindowsCapabilitySIDPath(home), bytes, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	caps, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if caps.Offline == "" {
		t.Fatal("upgrade must mint an offline-marker SID for a legacy file")
	}
	if caps.ReadOnly != "S-1-ro" {
		t.Errorf("upgrade must preserve existing ReadOnly SID, got %q", caps.ReadOnly)
	}
	// Idempotent: reload returns the same persisted Offline SID.
	again, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.Offline != caps.Offline {
		t.Errorf("offline SID not stable across reload: %q vs %q", again.Offline, caps.Offline)
	}
}
