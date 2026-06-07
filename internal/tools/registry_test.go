package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestCoreReadOnlyToolsExposeSafeMetadata(t *testing.T) {
	toolset := CoreReadOnlyTools(t.TempDir())
	if len(toolset) != 4 {
		t.Fatalf("expected 4 core read-only tools, got %d", len(toolset))
	}

	for _, tool := range toolset {
		if tool.Name() == "" {
			t.Fatalf("tool has empty name")
		}
		if tool.Description() == "" {
			t.Fatalf("%s has empty description", tool.Name())
		}
		if tool.Safety().SideEffect != SideEffectRead {
			t.Fatalf("%s side effect = %s, want read", tool.Name(), tool.Safety().SideEffect)
		}
		if tool.Safety().Permission != PermissionAllow {
			t.Fatalf("%s permission = %s, want allow", tool.Name(), tool.Safety().Permission)
		}
		if tool.Safety().Reason == "" {
			t.Fatalf("%s has empty safety reason", tool.Name())
		}

		schema := tool.Parameters()
		if schema.Type != "object" {
			t.Fatalf("%s schema type = %s, want object", tool.Name(), schema.Type)
		}
		if schema.Properties == nil {
			t.Fatalf("%s schema properties are nil", tool.Name())
		}
		if schema.AdditionalProperties {
			t.Fatalf("%s schema should disallow additional properties", tool.Name())
		}
	}
}

func TestCoreNetworkToolsExposePromptMetadata(t *testing.T) {
	toolset := CoreNetworkTools()
	if len(toolset) != 1 {
		t.Fatalf("expected 1 core network tool, got %d", len(toolset))
	}

	tool := toolset[0]
	if tool.Name() != "web_fetch" {
		t.Fatalf("expected web_fetch network tool, got %q", tool.Name())
	}
	safety := tool.Safety()
	if safety.SideEffect != SideEffectNetwork || safety.Permission != PermissionPrompt || !safety.AdvertiseInAuto {
		t.Fatalf("unexpected web_fetch safety metadata: %#v", safety)
	}
	schema := tool.Parameters()
	if schema.Properties == nil {
		t.Fatal("web_fetch schema properties are nil")
	}
	urlProperty, ok := schema.Properties["url"]
	if !ok {
		t.Fatal("web_fetch schema missing url property")
	}
	if urlProperty.Type != "string" {
		t.Fatalf("web_fetch url type = %s, want string", urlProperty.Type)
	}
}

func TestCoreToolsIncludeWebFetchButReadOnlyToolsDoNot(t *testing.T) {
	readOnly := CoreReadOnlyTools(t.TempDir())
	for _, tool := range readOnly {
		if tool.Name() == "web_fetch" {
			t.Fatal("web_fetch should not be exposed by read-only core tools")
		}
	}

	found := false
	for _, tool := range CoreTools(t.TempDir()) {
		if tool.Name() == "web_fetch" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected CoreTools to include web_fetch")
	}
}

func TestRegistryRunsToolsThroughSafePath(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewReadFileTool(t.TempDir()))

	result := registry.Run(context.Background(), "read_file", map[string]any{
		"path": "missing.txt",
	})

	if result.Status != StatusError {
		t.Fatalf("expected read error status, got %s", result.Status)
	}
	if result.Output == "" {
		t.Fatalf("expected an error output")
	}
}

func TestRegistryRequiresPermissionForWebFetch(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewWebFetchTool())

	result := registry.Run(context.Background(), "web_fetch", map[string]any{
		"url": "https://example.com",
	})

	if result.Status != StatusError {
		t.Fatalf("expected permission error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "Permission required for web_fetch") {
		t.Fatalf("unexpected permission output: %q", result.Output)
	}
}

func TestRegistryReportsUnknownTools(t *testing.T) {
	result := NewRegistry().Run(context.Background(), "missing", map[string]any{})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if result.Output != `Error: Unknown tool "missing".` {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestRegistryAppliesSandboxBeforeToolExecution(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")
	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
	})

	result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":      outside,
		"content":   "escape",
		"overwrite": true,
	}, RunOptions{
		PermissionGranted: true,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionUnsafe),
		Autonomy:          string(sandbox.AutonomyHigh),
	})

	if result.Status != StatusError {
		t.Fatalf("expected sandbox violation status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "Sandbox violation") || !strings.Contains(result.Output, "outside_workspace") {
		t.Fatalf("unexpected sandbox violation output: %q", result.Output)
	}
}

func TestRegistryAllowsPromptToolWithPersistentSandboxGrant(t *testing.T) {
	root := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json"),
	})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	if _, err := store.Grant(sandbox.GrantInput{
		ToolName:    "write_file",
		Decision:    sandbox.GrantAllow,
		MaxAutonomy: sandbox.AutonomyMedium,
		Reason:      "workspace writes",
	}); err != nil {
		t.Fatalf("Grant returned error: %v", err)
	}

	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Store:         store,
	})

	result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":      "granted.txt",
		"content":   "granted",
		"overwrite": true,
	}, RunOptions{
		PermissionGranted: false,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
		Autonomy:          string(sandbox.AutonomyMedium),
	})

	if result.Status != StatusOK {
		t.Fatalf("expected persistent sandbox grant to authorize write_file, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "granted.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(content) != "granted" {
		t.Fatalf("written content = %q, want granted", string(content))
	}
}
