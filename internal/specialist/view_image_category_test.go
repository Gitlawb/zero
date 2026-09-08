package specialist

import (
	"slices"
	"testing"
)

// view_image is a pure workspace-scoped read, so every category that already
// grants read_file should grant it too. Categories are what specialist authors
// actually write ("tools: [execute]"), so an omission here silently denies a
// specialist the ability to look at a screenshot it just captured.
func TestToolCategoriesIncludeViewImageWhereverReadFileIsGranted(t *testing.T) {
	for category, names := range toolCategories {
		if !slices.Contains(names, "read_file") {
			continue
		}
		if !slices.Contains(names, "view_image") {
			t.Errorf("category %q grants read_file but not view_image", category)
		}
	}
}

func TestResolveToolsExpandsExecuteToIncludeViewImage(t *testing.T) {
	resolved, err := ResolveTools([]string{"execute"})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	if !slices.Contains(resolved, "view_image") {
		t.Errorf("execute resolved to %v, missing view_image", resolved)
	}
}
