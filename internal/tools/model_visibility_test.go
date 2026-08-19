package tools

import "testing"

func TestToolSearchDoesNotSurfaceLegacyStringReplacement(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewScopedEditFileTool(t.TempDir(), nil))
	registry.Register(NewScopedApplyPatchTool(t.TempDir(), nil))
	search := NewToolSearchTool(registry).(toolSearchTool)

	eager := search.visibleEagerToolNames(nil, nil, "ask")
	if eager["edit_file"] {
		t.Fatal("tool_search must not describe edit_file as available to the model")
	}
	if !eager["apply_patch"] {
		t.Fatal("tool_search must retain apply_patch as the available edit tool")
	}
}
