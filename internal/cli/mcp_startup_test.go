package cli

import (
	"context"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestSplitMCPStartupConfigDefersOnlyUnconfiguredDefaults(t *testing.T) {
	defaults := config.DefaultMCPServers()
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"firecrawl": defaults["firecrawl"],
		"docs":      {Type: "http", URL: "https://docs.example.test/mcp"},
		"disabled":  {Type: "http", URL: "https://disabled.example.test/mcp", Disabled: true},
	}}

	critical, optional := splitMCPStartupConfig(cfg)
	if _, ok := critical.Servers["docs"]; !ok || len(critical.Servers) != 1 {
		t.Fatalf("critical servers = %#v, want only docs", critical.Servers)
	}
	if _, ok := optional.Servers["firecrawl"]; !ok || len(optional.Servers) != 1 {
		t.Fatalf("optional servers = %#v, want only firecrawl", optional.Servers)
	}
}

func TestOptionalMCPAwaitTimesOutThenObservesPublishedTools(t *testing.T) {
	registry := tools.NewRegistry()
	release := make(chan struct{})
	started := make(chan struct{})
	startup := startOptionalMCP(
		context.Background(),
		registry,
		config.MCPConfig{Servers: config.DefaultMCPServers()},
		mcp.RegisterOptions{},
		func(ctx context.Context, registry *tools.Registry, _ config.MCPConfig, _ mcp.RegisterOptions) (mcpToolRuntime, error) {
			close(started)
			select {
			case <-release:
				registry.RegisterBatch([]tools.Tool{tools.NewScopedReadFileTool(t.TempDir(), nil)})
				return noopMCPRuntime{}, nil
			case <-ctx.Done():
				return noopMCPRuntime{}, ctx.Err()
			}
		},
		nil,
	)
	defer func() { _ = startup.Close() }()
	<-started

	if startup.Await(t.Context(), time.Millisecond) {
		t.Fatal("optional startup unexpectedly completed before release")
	}
	if got := len(registry.All()); got != 0 {
		t.Fatalf("registry published tools before readiness: %d", got)
	}
	close(release)
	if !startup.Await(t.Context(), time.Second) {
		t.Fatal("optional startup did not complete after release")
	}
	if _, ok := registry.Get("read_file"); !ok {
		t.Fatal("completed optional startup did not publish its tool batch")
	}
}

func TestOptionalMCPCloseCancelsBlockedStartup(t *testing.T) {
	started := make(chan struct{})
	startup := startOptionalMCP(
		context.Background(),
		tools.NewRegistry(),
		config.MCPConfig{Servers: config.DefaultMCPServers()},
		mcp.RegisterOptions{},
		func(ctx context.Context, _ *tools.Registry, _ config.MCPConfig, _ mcp.RegisterOptions) (mcpToolRuntime, error) {
			close(started)
			<-ctx.Done()
			return noopMCPRuntime{}, nil
		},
		nil,
	)
	<-started
	if err := startup.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
