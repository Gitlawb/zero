package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zerocommands"
)

func TestRunBackendsJSONUsesLifecycleSnapshotWithoutConnectingMCP(t *testing.T) {
	cwd := t.TempDir()
	secret := "sk-proj-" + strings.Repeat("a", 24)
	deps := appDeps{
		getwd: func() (string, error) { return cwd, nil },
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			return config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"zulu": {
					Type: "http",
					URL:  "https://admin:secret@example.com/mcp?token=" + secret + "&mode=readonly",
					Headers: map[string]string{
						"Authorization": "Bearer " + secret,
					},
				},
				"alpha": {
					Type:    "stdio",
					Command: "alpha-mcp",
					Args:    []string{"--project", cwd},
					Env: map[string]string{
						"ALPHA_TOKEN": secret,
					},
				},
				"disabled": {
					Type:     "stdio",
					Command:  "disabled-mcp",
					Disabled: true,
				},
			}}, nil
		},
		loadHooks: func(options hooks.LoadOptions) (hooks.LoadResult, error) {
			if options.Cwd != cwd {
				t.Fatalf("hook Cwd = %q, want %q", options.Cwd, cwd)
			}
			return hooks.LoadResult{Config: hooks.Config{Hooks: []hooks.Definition{{
				ID:      "zero.preflight",
				Event:   hooks.EventBeforeTool,
				Matcher: "bash",
				Command: "sh",
				Args:    []string{"-c", "echo " + secret},
				Enabled: true,
			}}}}, nil
		},
		loadPlugins: func(options plugins.LoadOptions) (plugins.LoadResult, error) {
			if options.Cwd != cwd {
				t.Fatalf("plugin Cwd = %q, want %q", options.Cwd, cwd)
			}
			return plugins.LoadResult{Plugins: []plugins.LoadedPlugin{{
				ID:           "zero.docs",
				Name:         "Docs",
				Description:  "uses " + secret,
				Enabled:      true,
				Source:       plugins.SourceProject,
				Root:         "C:/tmp/" + secret,
				PluginDir:    "C:/tmp/plugin",
				ManifestPath: "C:/tmp/plugin/plugin.json?token=" + secret,
				Tools:        []plugins.ToolExtension{{Name: "lookup"}},
				Prompts:      []plugins.PathExtension{{Name: "review"}},
				Hooks:        []plugins.HookExtension{{Name: "audit"}},
			}}}, nil
		},
		registerMCPTools: func(context.Context, *tools.Registry, config.MCPConfig, mcp.RegisterOptions) (mcpToolRuntime, error) {
			return nil, errors.New("zero backends must not connect to MCP servers")
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"backends", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stdout.String(), "admin:secret") || strings.Contains(stdout.String(), "Authorization") || strings.Contains(stdout.String(), "ALPHA_TOKEN") {
		t.Fatalf("backend JSON leaked secret material:\n%s", stdout.String())
	}

	var snapshot zerocommands.BackendLifecycleSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("backend JSON failed to decode: %v\n%s", err, stdout.String())
	}
	if len(snapshot.MCPServers) != 2 || snapshot.MCPServers[0].Name != "alpha" || snapshot.MCPServers[1].Name != "zulu" {
		t.Fatalf("unexpected MCP snapshots: %#v", snapshot.MCPServers)
	}
	if snapshot.MCPServers[0].ArgCount != 2 || snapshot.MCPServers[0].EnvKeyCount != 1 {
		t.Fatalf("stdio MCP counts wrong: %#v", snapshot.MCPServers[0])
	}
	if snapshot.MCPServers[1].HeaderCount != 1 || !strings.Contains(snapshot.MCPServers[1].URL, "mode=readonly") {
		t.Fatalf("http MCP snapshot missing safe status data: %#v", snapshot.MCPServers[1])
	}
	if len(snapshot.Hooks) != 1 || snapshot.Hooks[0].ID != "zero.preflight" || strings.Contains(strings.Join(snapshot.Hooks[0].Args, " "), secret) {
		t.Fatalf("unexpected hook snapshots: %#v", snapshot.Hooks)
	}
	if len(snapshot.Plugins) != 1 || snapshot.Plugins[0].ID != "zero.docs" || snapshot.Plugins[0].ToolCount != 1 || snapshot.Plugins[0].PromptCount != 1 || snapshot.Plugins[0].HookCount != 1 {
		t.Fatalf("unexpected plugin snapshots: %#v", snapshot.Plugins)
	}
}

func TestRunBackendsTextAndHelp(t *testing.T) {
	deps := appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
		resolveMCPConfig: func(string) (config.MCPConfig, error) {
			return config.MCPConfig{}, nil
		},
		loadHooks: func(hooks.LoadOptions) (hooks.LoadResult, error) {
			return hooks.LoadResult{}, nil
		},
		loadPlugins: func(plugins.LoadOptions) (plugins.LoadResult, error) {
			return plugins.LoadResult{}, nil
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"backends"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	for _, want := range []string{"Zero Backends:", "MCP servers: 0", "Hooks: 0", "Plugins: 0"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("backend text missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"backends", "--help"}, &stdout, &stderr, appDeps{})
	if exitCode != exitSuccess {
		t.Fatalf("help exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	for _, want := range []string{"Usage:", "zero backends", "--json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("backend help missing %q:\n%s", want, stdout.String())
		}
	}
}
