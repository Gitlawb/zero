package plugins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestNormalizesExtensionsAndPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	pluginDir := filepath.Join(root, "zero-demo")
	manifestPath := filepath.Join(pluginDir, "plugin.json")

	plugin, err := ParseManifest(map[string]any{
		"schemaVersion": float64(1),
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
		"description":   "Demo plugin",
		"tools": []any{map[string]any{
			"name":        "lookup",
			"description": "Lookup docs",
			"command":     "node",
			"args":        []any{"tools/lookup.mjs"},
			"inputSchema": map[string]any{"type": "object"},
			"permission":  "prompt",
		}},
		"prompts": []any{map[string]any{"name": "review", "path": "prompts/review.md"}},
		"skills":  []any{map[string]any{"name": "ts-review", "path": "skills/ts-review/SKILL.md"}},
		"hooks": []any{map[string]any{
			"name":    "pre-tool",
			"event":   "beforeTool",
			"command": "node",
			"args":    []any{"hooks/pre-tool.mjs"},
		}},
	}, ParseManifestOptions{
		Source:       SourceProject,
		Root:         root,
		PluginDir:    pluginDir,
		ManifestPath: manifestPath,
	})
	if err != nil {
		t.Fatalf("ParseManifest returned error: %v", err)
	}

	if plugin.ID != "zero.demo" || plugin.Name != "Zero Demo" || !plugin.Enabled {
		t.Fatalf("unexpected plugin metadata: %#v", plugin)
	}
	if plugin.Tools[0].Permission != PermissionPrompt || plugin.Tools[0].Args[0] != "tools/lookup.mjs" {
		t.Fatalf("unexpected tool normalization: %#v", plugin.Tools[0])
	}
	if plugin.Prompts[0].Path != filepath.Join(pluginDir, "prompts", "review.md") {
		t.Fatalf("prompt path = %q", plugin.Prompts[0].Path)
	}
	if plugin.Skills[0].Path != filepath.Join(pluginDir, "skills", "ts-review", "SKILL.md") {
		t.Fatalf("skill path = %q", plugin.Skills[0].Path)
	}
	if plugin.Hooks[0].Event != HookBeforeTool || plugin.Hooks[0].Args[0] != "hooks/pre-tool.mjs" {
		t.Fatalf("unexpected hook normalization: %#v", plugin.Hooks[0])
	}
}

func TestParseManifestClampsAutoApprovalByDefault(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "zero-demo")
	manifest := map[string]any{
		"schemaVersion": float64(1),
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
		"tools": []any{map[string]any{
			"name":       "lookup",
			"command":    "node",
			"permission": "allow",
		}},
	}
	options := ParseManifestOptions{
		Source:       SourceProject,
		Root:         root,
		PluginDir:    pluginDir,
		ManifestPath: filepath.Join(pluginDir, "plugin.json"),
	}

	plugin, err := ParseManifest(manifest, options)
	if err != nil {
		t.Fatalf("ParseManifest returned error: %v", err)
	}
	if got := plugin.Tools[0].Permission; got != PermissionPrompt {
		t.Fatalf("permission = %s, want prompt", got)
	}

	options.AllowManifestToolAutoApproval = true
	plugin, err = ParseManifest(manifest, options)
	if err != nil {
		t.Fatalf("ParseManifest returned error: %v", err)
	}
	if got := plugin.Tools[0].Permission; got != PermissionAllow {
		t.Fatalf("permission = %s, want allow", got)
	}
}

func TestParseManifestRejectsUnsafePluginLocalPaths(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "bad")
	options := ParseManifestOptions{
		Source:       SourceProject,
		Root:         root,
		PluginDir:    pluginDir,
		ManifestPath: filepath.Join(pluginDir, "plugin.json"),
	}

	for _, path := range []string{"../outside.md", "/tmp/escape.md", `C:\Windows\escape.md`} {
		t.Run(path, func(t *testing.T) {
			_, err := ParseManifest(map[string]any{
				"schemaVersion": float64(1),
				"id":            "zero.bad",
				"name":          "Bad",
				"version":       "0.1.0",
				"prompts":       []any{map[string]any{"name": "escape", "path": path}},
			}, options)
			if err == nil || !strings.Contains(err.Error(), "must stay inside the plugin directory") {
				t.Fatalf("expected unsafe path error, got %v", err)
			}
		})
	}
}

func TestParseManifestRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "bad")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "escape.md"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(pluginDir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	_, err := ParseManifest(map[string]any{
		"schemaVersion": float64(1),
		"id":            "zero.bad",
		"name":          "Bad",
		"version":       "0.1.0",
		"prompts":       []any{map[string]any{"name": "escape", "path": filepath.Join("link", "escape.md")}},
	}, ParseManifestOptions{
		Source:       SourceProject,
		Root:         root,
		PluginDir:    pluginDir,
		ManifestPath: filepath.Join(pluginDir, "plugin.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "must stay inside the plugin directory") {
		t.Fatalf("expected symlink escape error, got %v", err)
	}
}

func TestParseManifestRejectsSymlinkEscapesWithMissingLeaf(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "bad")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(pluginDir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	_, err := ParseManifest(map[string]any{
		"schemaVersion": float64(1),
		"id":            "zero.bad",
		"name":          "Bad",
		"version":       "0.1.0",
		"prompts":       []any{map[string]any{"name": "escape", "path": filepath.Join("link", "missing.md")}},
	}, ParseManifestOptions{
		Source:       SourceProject,
		Root:         root,
		PluginDir:    pluginDir,
		ManifestPath: filepath.Join(pluginDir, "plugin.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "must stay inside the plugin directory") {
		t.Fatalf("expected missing-leaf symlink escape error, got %v", err)
	}
}

func TestFormatListIncludesDiagnosticLocations(t *testing.T) {
	output := FormatList(nil, []Diagnostic{{
		Kind:         DiagnosticSchema,
		Message:      "bad prompt path",
		ManifestPath: "/plugins/bad/plugin.json",
		FieldPath:    "prompts.escape.path",
	}})
	for _, want := range []string{"[schema] bad prompt path", "manifestPath=/plugins/bad/plugin.json", "fieldPath=prompts.escape.path"} {
		if !strings.Contains(output, want) {
			t.Fatalf("diagnostic output missing %q: %s", want, output)
		}
	}
}

func TestToDiagnosticClassifiesManifestAndIOErrors(t *testing.T) {
	root := Root{Source: SourceProject}
	schemaDiagnostic := toDiagnostic(ManifestError{FieldPath: "prompts.0.path", Message: "bad path"}, root, "/plugins", "/plugins/bad", "/plugins/bad/plugin.json")
	if schemaDiagnostic.Kind != DiagnosticSchema || schemaDiagnostic.FieldPath != "prompts.0.path" {
		t.Fatalf("manifest error diagnostic = %#v", schemaDiagnostic)
	}

	ioDiagnostic := toDiagnostic(errors.New("read failed"), root, "/plugins", "/plugins/bad", "/plugins/bad/plugin.json")
	if ioDiagnostic.Kind != DiagnosticIO || ioDiagnostic.Message != "read failed" {
		t.Fatalf("io error diagnostic = %#v", ioDiagnostic)
	}
}

func TestLoadPluginsDiscoversDiagnosticsAndProjectPrecedence(t *testing.T) {
	dir := t.TempDir()
	userRoot := filepath.Join(dir, "user-plugins")
	projectRoot := filepath.Join(dir, "project-plugins")
	writePluginManifest(t, filepath.Join(userRoot, "demo"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "User Demo",
		"version":       "0.1.0",
	})
	writePluginManifest(t, filepath.Join(projectRoot, "demo"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Project Demo",
		"version":       "0.2.0",
		"enabled":       false,
	})
	writePluginManifest(t, filepath.Join(projectRoot, "docs"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.docs",
		"name":          "Docs",
		"version":       "1.0.0",
	})
	if err := os.MkdirAll(filepath.Join(projectRoot, "bad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "bad", "plugin.json"), []byte("{ invalid json }"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Load(LoadOptions{
		Roots: []Root{
			{Source: SourceUser, Path: userRoot},
			{Source: SourceProject, Path: projectRoot},
		},
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := []string{result.Plugins[0].ID + ":" + result.Plugins[0].Name, result.Plugins[1].ID + ":" + result.Plugins[1].Name}; got[0] != "zero.demo:Project Demo" || got[1] != "zero.docs:Docs" {
		t.Fatalf("unexpected plugins: %#v", result.Plugins)
	}
	if result.Plugins[0].Enabled {
		t.Fatalf("project plugin should remain disabled")
	}
	if !hasPluginDiagnostic(result.Diagnostics, DiagnosticDuplicate, "zero.demo") {
		t.Fatalf("missing duplicate diagnostic: %#v", result.Diagnostics)
	}
	if !hasPluginDiagnostic(result.Diagnostics, DiagnosticJSON, "") {
		t.Fatalf("missing json diagnostic: %#v", result.Diagnostics)
	}
}

func TestResolveRootsUsesConfigHomeAndProjectRoot(t *testing.T) {
	dir := t.TempDir()
	roots, err := ResolveRoots(ResolveRootOptions{
		Cwd: dir,
		Env: map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "xdg")},
	})
	if err != nil {
		t.Fatalf("ResolveRoots returned error: %v", err)
	}
	if roots[0].Path != filepath.Join(dir, "xdg", "zero", "plugins") {
		t.Fatalf("user root = %q", roots[0].Path)
	}
	if roots[1].Path != filepath.Join(dir, ".zero", "plugins") {
		t.Fatalf("project root = %q", roots[1].Path)
	}
}

func writePluginManifest(t *testing.T, pluginDir string, manifest map[string]any) {
	t.Helper()
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasPluginDiagnostic(diagnostics []Diagnostic, kind DiagnosticKind, pluginID string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind != kind {
			continue
		}
		if pluginID == "" || diagnostic.PluginID == pluginID {
			return true
		}
	}
	return false
}
