package modelregistry

import "strings"

// UpgradeTarget resolves the stronger model that id escalates to during mid-run
// escalation. It resolves id to a concrete entry, reads its UpgradeTargetID,
// resolves THAT to a concrete entry, and returns it only when the target is
// available and not deprecated. It returns (_, false) when id is unknown, has no
// configured target, or the target resolves to an unavailable/deprecated model.
// Pure and registry-only: applies no deprecation fallback redirection.
func (registry Registry) UpgradeTarget(id string) (ModelEntry, bool) {
	source, ok := registry.Resolve(id)
	if !ok {
		return ModelEntry{}, false
	}
	targetID := strings.TrimSpace(source.UpgradeTargetID)
	if targetID == "" {
		return ModelEntry{}, false
	}
	target, ok := registry.Resolve(targetID)
	if !ok {
		return ModelEntry{}, false
	}
	if target.Status == ModelStatusDeprecated {
		return ModelEntry{}, false
	}
	return target, true
}
