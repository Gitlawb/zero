package swarm

import (
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

// All swarm tools must be deferred-eligible so their schemas stay out of the
// eager per-request tool prefix (loaded on demand via tool_search), while the
// core built-ins remain eager.
func TestSwarmToolsAreDeferred(t *testing.T) {
	registry := tools.NewRegistry()
	RegisterTools(registry, &Swarm{})
	for _, tool := range registry.All() {
		if !tools.IsDeferred(tool) {
			t.Errorf("swarm tool %q is NOT deferred-eligible (would bloat the eager prefix)", tool.Name())
		}
	}
	if len(registry.All()) < 6 {
		t.Fatalf("expected the swarm tools registered, got %d", len(registry.All()))
	}
}
