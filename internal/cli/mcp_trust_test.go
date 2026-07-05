package cli

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/workspacetrust"
)

// mcpTrustDeps builds an appDeps whose resolveMCPConfig HONORS excludeProject: it
// returns a project stdio server only when excludeProject is false, mirroring the
// real ResolveMCP gate. It records the excludeProject value it was called with and
// whether registerMCPTools (which spawns servers) actually fired.
func mcpTrustDeps(gotExclude *bool, spawned *bool) appDeps {
	return appDeps{
		resolveMCPConfig: func(_ string, excludeProject bool) (config.MCPConfig, error) {
			*gotExclude = excludeProject
			servers := map[string]config.MCPServerConfig{}
			if !excludeProject {
				servers["proj-srv"] = config.MCPServerConfig{Type: "stdio", Command: "proj-cmd"}
			}
			return config.MCPConfig{Servers: servers}, nil
		},
		newMCPStore: func() (*mcp.PermissionStore, error) { return nil, nil },
		registerMCPTools: func(_ context.Context, _ *tools.Registry, _ config.MCPConfig, _ mcp.RegisterOptions) (mcpToolRuntime, error) {
			*spawned = true
			return closeFunc(func() error { return nil }), nil
		},
	}
}

// TestMCPGateUntrustedExcludesProjectServer proves the P0 fix: an untrusted trustRoot
// makes registerMCPToolsForWorkspace resolve MCP config with excludeProject=true and,
// because that drops the project server, it never spawns anything. This test is
// load-bearing: if the gate is removed (a hardcoded excludeProject=false passed to
// resolveMCPConfig), gotExclude is false, the project server survives, spawned flips
// true, and every assertion below fails.
func TestMCPGateUntrustedExcludesProjectServer(t *testing.T) {
	setTrustConfigRoot(t)
	repo := t.TempDir() // never trusted

	var gotExclude, spawned bool
	deps := mcpTrustDeps(&gotExclude, &spawned)
	registry := tools.NewRegistry()

	runtime, err := registerMCPToolsForWorkspace(context.Background(), repo, registry, deps, mcp.AutonomyLow, repo)
	if err != nil {
		t.Fatalf("registerMCPToolsForWorkspace: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	if !gotExclude {
		t.Fatalf("untrusted workspace must resolve MCP config with excludeProject=true")
	}
	if spawned {
		t.Fatalf("untrusted workspace must not spawn the project MCP server")
	}
	if _, ok := runtime.(noopMCPRuntime); !ok {
		t.Fatalf("with the project server dropped, the runtime should be the noop runtime, got %T", runtime)
	}
}

// TestMCPGateEmptyTrustRootFailsClosed proves fail-closed-by-construction: a caller
// that forgot to resolve trustRoot (empty) still excludes the project layer.
func TestMCPGateEmptyTrustRootFailsClosed(t *testing.T) {
	setTrustConfigRoot(t)
	repo := t.TempDir()
	// Even trusting the repo must not help when the caller passes an empty root.
	if err := workspacetrust.Trust(repo); err != nil {
		t.Fatalf("Trust(repo): %v", err)
	}

	var gotExclude, spawned bool
	deps := mcpTrustDeps(&gotExclude, &spawned)
	registry := tools.NewRegistry()

	runtime, err := registerMCPToolsForWorkspace(context.Background(), repo, registry, deps, mcp.AutonomyLow, "")
	if err != nil {
		t.Fatalf("registerMCPToolsForWorkspace: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	if !gotExclude {
		t.Fatalf("empty trustRoot must fail closed (excludeProject=true)")
	}
	if spawned {
		t.Fatalf("empty trustRoot must not spawn the project MCP server")
	}
}

// TestMCPGateTrustedSpawnsProjectServer proves R3 for MCP: after Trust(repo) the
// project layer is included (excludeProject=false) and the project server spawns.
func TestMCPGateTrustedSpawnsProjectServer(t *testing.T) {
	setTrustConfigRoot(t)
	repo := t.TempDir()
	if err := workspacetrust.Trust(repo); err != nil {
		t.Fatalf("Trust(repo): %v", err)
	}

	var gotExclude, spawned bool
	deps := mcpTrustDeps(&gotExclude, &spawned)
	registry := tools.NewRegistry()

	runtime, err := registerMCPToolsForWorkspace(context.Background(), repo, registry, deps, mcp.AutonomyLow, repo)
	if err != nil {
		t.Fatalf("registerMCPToolsForWorkspace: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	if gotExclude {
		t.Fatalf("trusted workspace must resolve MCP config with excludeProject=false")
	}
	if !spawned {
		t.Fatalf("trusted workspace must spawn the project MCP server")
	}
}
