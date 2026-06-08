package modelregistry

// SupportsVision reports whether the model identified by modelID is known to the
// registry and advertises the vision capability. It returns false when the model
// cannot be resolved (e.g. a custom or openai-compatible id absent from the
// catalog): an unknown model is treated as "cannot confirm", so callers drop
// images rather than send them to a model that may reject them.
//
// modelID is resolved through the registry's normal alias/pattern matching, so
// any spelling that Get accepts works here too. This helper is the single
// capability check shared by the headless (exec) and interactive (TUI) input
// surfaces; the warn-and-drop behavior lives at those call sites.
func SupportsVision(registry Registry, modelID string) bool {
	return registry.SupportsCapability(modelID, ModelCapabilityVision)
}
