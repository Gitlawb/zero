package modelregistry

import (
	"strings"
	"testing"
)

// upgradeEntry builds a minimal valid active entry with an upgrade target set.
func upgradeEntry(id, alias, upgradeTargetID string) ModelEntry {
	entry := mkEntry(id, alias)
	entry.UpgradeTargetID = upgradeTargetID
	return entry
}

func upgradeTestRegistry(t *testing.T) Registry {
	t.Helper()
	haiku := upgradeEntry("claude-haiku-4-5", "haiku-4.5", "claude-sonnet-4-5")
	sonnet := upgradeEntry("claude-sonnet-4-5", "sonnet-4.5", "claude-opus-4-1")
	opus := mkEntry("claude-opus-4-1", "opus-4.1") // top-tier: no UpgradeTargetID

	// A model whose target points at a deprecated entry must not escalate.
	legacy := upgradeEntry("legacy-mini", "legacy-m", "legacy-retired")
	retired := mkEntry("legacy-retired", "legacy-r")
	retired.Status = ModelStatusDeprecated
	retired.Deprecation = &DeprecationRule{FallbackID: "claude-opus-4-1", WarningMsg: "retired"}

	reg, err := NewRegistry([]ModelEntry{haiku, sonnet, opus, legacy, retired})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	return reg
}

func TestUpgradeTargetResolvesConcreteEntry(t *testing.T) {
	reg := upgradeTestRegistry(t)

	target, ok := reg.UpgradeTarget("claude-haiku-4-5")
	if !ok {
		t.Fatal("haiku should escalate to its upgrade target")
	}
	if target.ID != "claude-sonnet-4-5" {
		t.Fatalf("UpgradeTarget(haiku) = %q, want claude-sonnet-4-5", target.ID)
	}

	// Resolution should also work through an alias for the source model.
	if target, ok := reg.UpgradeTarget("sonnet-4.5"); !ok || target.ID != "claude-opus-4-1" {
		t.Fatalf("UpgradeTarget(sonnet alias) = %q/%v, want claude-opus-4-1/true", target.ID, ok)
	}
}

func TestUpgradeTargetTopTierHasNone(t *testing.T) {
	reg := upgradeTestRegistry(t)
	if target, ok := reg.UpgradeTarget("claude-opus-4-1"); ok {
		t.Fatalf("top-tier model should have no upgrade target, got %q", target.ID)
	}
}

func TestUpgradeTargetUnknownSource(t *testing.T) {
	reg := upgradeTestRegistry(t)
	if _, ok := reg.UpgradeTarget("not-a-real-model"); ok {
		t.Fatal("unknown source model should not yield an upgrade target")
	}
}

func TestUpgradeTargetDeprecatedTargetRejected(t *testing.T) {
	reg := upgradeTestRegistry(t)
	if target, ok := reg.UpgradeTarget("legacy-mini"); ok {
		t.Fatalf("deprecated target should be rejected, got %q", target.ID)
	}
}

func TestNewRegistryRejectsDanglingUpgradeTarget(t *testing.T) {
	// A non-empty UpgradeTargetID that does not resolve to a known model is a
	// catalog typo; NewRegistry must fail loudly rather than silently disabling
	// escalation at runtime (mirrors the deprecation-fallback validation).
	dangling := upgradeEntry("dangling-mini", "dangling-m", "does-not-exist")
	_, err := NewRegistry([]ModelEntry{dangling})
	if err == nil {
		t.Fatal("NewRegistry should reject an unresolvable upgrade target")
	}
	if !strings.Contains(err.Error(), "upgrade target") {
		t.Fatalf("error %q should mention the upgrade target", err.Error())
	}
}

func TestUpgradeTargetReturnsIndependentCopy(t *testing.T) {
	reg := upgradeTestRegistry(t)
	target, ok := reg.UpgradeTarget("claude-haiku-4-5")
	if !ok {
		t.Fatal("haiku should escalate")
	}
	target.Aliases = append(target.Aliases, "mutated")
	again, _ := reg.UpgradeTarget("claude-haiku-4-5")
	for _, alias := range again.Aliases {
		if alias == "mutated" {
			t.Fatal("UpgradeTarget must return a defensive copy, not a shared entry")
		}
	}
}
