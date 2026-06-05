package mcp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestRegisterToolsAddsPromptGatedMCPTools(t *testing.T) {
	registry := tools.NewRegistry()
	client := &fakeToolClient{listed: []RemoteTool{{
		Name:        "lookup",
		Description: "Lookup documentation",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
	}}}

	runtime, err := RegisterTools(context.Background(), registry, config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp"},
	}}, RegisterOptions{
		ClientFactory: func(context.Context, Server) (ToolClient, error) {
			return client, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterTools() error = %v", err)
	}
	defer runtime.Close()

	tool, ok := registry.Get("mcp_docs_lookup")
	if !ok {
		t.Fatal("expected mcp_docs_lookup to be registered")
	}
	if tool.Safety().Permission != tools.PermissionPrompt {
		t.Fatalf("Safety.Permission = %q, want prompt", tool.Safety().Permission)
	}
	if tool.Safety().SideEffect != tools.SideEffectNetwork {
		t.Fatalf("Safety.SideEffect = %q, want network", tool.Safety().SideEffect)
	}

	denied := registry.Run(context.Background(), "mcp_docs_lookup", map[string]any{"query": "zero"})
	if denied.Status != tools.StatusError {
		t.Fatalf("Run without approval = %#v, want permission error", denied)
	}
	approved := registry.RunWithOptions(context.Background(), "mcp_docs_lookup", map[string]any{"query": "zero"}, tools.RunOptions{PermissionGranted: true})
	if approved.Status != tools.StatusOK || approved.Output != "lookup: zero" {
		t.Fatalf("approved run = %#v, want lookup output", approved)
	}
	if approved.Meta["mcp.server"] != "docs" || approved.Meta["mcp.tool"] != "lookup" {
		t.Fatalf("approved meta = %#v, want mcp server/tool", approved.Meta)
	}
	if client.closed != 0 {
		t.Fatalf("client.closed before runtime close = %d, want 0", client.closed)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Runtime.Close() error = %v", err)
	}
	if client.closed != 1 {
		t.Fatalf("client.closed after runtime close = %d, want 1", client.closed)
	}
}

func TestRegisterToolsMarksPersistentlyApprovedToolsAllow(t *testing.T) {
	store, err := NewPermissionStore(StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "permissions.json"),
		Now:      func() time.Time { return time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	servers, err := NormalizeConfig(config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GrantTool(GrantToolInput{
		ServerName:     "docs",
		ServerIdentity: servers[0].Identity,
		ToolName:       "lookup",
		MaxAutonomy:    AutonomyLow,
	}); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry()
	runtime, err := RegisterTools(context.Background(), registry, config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp"},
	}}, RegisterOptions{
		PermissionStore: store,
		Autonomy:        AutonomyLow,
		ClientFactory: func(context.Context, Server) (ToolClient, error) {
			return &fakeToolClient{listed: []RemoteTool{{Name: "lookup", Description: "Lookup documentation"}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterTools() error = %v", err)
	}
	defer runtime.Close()

	tool, ok := registry.Get("mcp_docs_lookup")
	if !ok {
		t.Fatal("expected mcp_docs_lookup to be registered")
	}
	if tool.Safety().Permission != tools.PermissionAllow {
		t.Fatalf("Safety.Permission = %q, want allow from persistent MCP grant", tool.Safety().Permission)
	}
}

type fakeToolClient struct {
	listed []RemoteTool
	closed int
}

func (client *fakeToolClient) ListTools(context.Context) ([]RemoteTool, error) {
	return client.listed, nil
}

func (client *fakeToolClient) CallTool(_ context.Context, _ string, args map[string]any) (CallToolResult, error) {
	return CallToolResult{
		Content: []Content{{Type: "text", Text: "lookup: " + args["query"].(string)}},
	}, nil
}

func (client *fakeToolClient) Close() error {
	client.closed++
	return nil
}
