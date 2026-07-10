package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBlocksTamperedManagedPlugin(t *testing.T) {
	root := t.TempDir()
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
		"prompts":       []any{map[string]any{"name": "review", "path": "review.md"}},
	})
	if err := os.WriteFile(filepath.Join(src, "review.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: root}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "zero.demo", "review.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Load(LoadOptions{Roots: []Root{{Source: SourceUser, Path: root}}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result.Plugins) != 0 {
		t.Fatalf("tampered managed plugin should be blocked: %#v", result.Plugins)
	}
	if !hasPluginDiagnostic(result.Diagnostics, DiagnosticIntegrity, "zero.demo") {
		t.Fatalf("missing integrity diagnostic: %#v", result.Diagnostics)
	}
}

func TestLoadAllowsUnmanagedPluginWithoutHash(t *testing.T) {
	root := t.TempDir()
	writePluginManifest(t, filepath.Join(root, "demo"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
	})

	result, err := Load(LoadOptions{Roots: []Root{{Source: SourceUser, Path: root}}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result.Plugins) != 1 || result.Plugins[0].ID != "zero.demo" {
		t.Fatalf("unmanaged plugin should load: %#v", result.Plugins)
	}
}
