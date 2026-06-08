package tui

import (
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
)

// modelSupportsVisionTUI reports whether the active model can accept image input.
// An unknown / custom id (not in the catalog) returns false: we cannot confirm
// vision support, so the TUI refuses to attach rather than silently sending
// images a model may reject. Mirrors the CLI/headless vision gate (component E).
func modelSupportsVisionTUI(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" {
		return false
	}
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return false
	}
	return modelregistry.SupportsVision(registry, trimmed)
}
