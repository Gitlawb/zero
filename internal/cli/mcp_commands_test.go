package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestRunMCPAddStdioPersistsUserConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	var stdout, stderr bytes.Buffer

	exitCode := runWithDeps([]string{"mcp", "add", "docs", "--env", "DOCS_TOKEN=secret", "--", "docs-mcp", "--port", "123"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})

	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	cfg := readMCPCommandConfig(t, configPath)
	server, ok := cfg.MCP.Servers["docs"]
	if !ok {
		t.Fatalf("docs server was not persisted: %#v", cfg.MCP.Servers)
	}
	if server.Type != "stdio" || server.Command != "docs-mcp" {
		t.Fatalf("server = %#v, want stdio docs-mcp", server)
	}
	if !reflect.DeepEqual(server.Args, []string{"--port", "123"}) {
		t.Fatalf("Args = %#v, want --port 123", server.Args)
	}
	if got := server.Env["DOCS_TOKEN"]; got != "secret" {
		t.Fatalf("Env[DOCS_TOKEN] = %q, want secret", got)
	}
	if strings.Contains(stdout.String(), "secret") {
		t.Fatalf("stdout leaked env value: %s", stdout.String())
	}
}

func TestRunMCPAddHTTPPreservesConfigAndRedactsHeader(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	writeMCPCommandRawConfig(t, configPath, `{
  "activeProvider": "fast",
  "futureTop": {"keep": true},
  "mcp": {
    "futureMCP": "keep",
    "servers": {
      "other": {
        "type": "http",
        "url": "https://other.example/mcp",
        "futureServer": "keep"
      }
    }
  }
}
`)
	var stdout, stderr bytes.Buffer

	exitCode := runWithDeps([]string{"mcp", "add", "remote", "--url", "https://remote.example/mcp", "--header", "Authorization=Bearer secret-header", "--json"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})

	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), "secret-header") {
		t.Fatalf("stdout leaked header value: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[REDACTED]") {
		t.Fatalf("stdout did not redact header value: %s", stdout.String())
	}
	cfg := readMCPCommandConfig(t, configPath)
	remote := cfg.MCP.Servers["remote"]
	if remote.Type != "http" || remote.URL != "https://remote.example/mcp" {
		t.Fatalf("remote server = %#v, want default http type with URL", remote)
	}
	if got := remote.Headers["Authorization"]; got != "Bearer secret-header" {
		t.Fatalf("Authorization header = %q, want persisted secret", got)
	}
	raw := readMCPCommandRawConfig(t, configPath)
	if _, ok := raw["futureTop"]; !ok {
		t.Fatalf("top-level unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, raw))
	}
	mcpRaw := rawMCPCommandObject(t, raw["mcp"])
	if _, ok := mcpRaw["futureMCP"]; !ok {
		t.Fatalf("mcp unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, mcpRaw))
	}
	serversRaw := rawMCPCommandObject(t, mcpRaw["servers"])
	otherRaw := rawMCPCommandObject(t, serversRaw["other"])
	if _, ok := otherRaw["futureServer"]; !ok {
		t.Fatalf("unrelated server unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, otherRaw))
	}
}

func TestRunMCPRemoveDeletesUserConfigServer(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	writeMCPCommandConfig(t, configPath, config.FileConfig{
		MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"docs":  {Type: "stdio", Command: "docs-mcp"},
			"other": {Type: "http", URL: "https://example.com/mcp"},
		}},
	})
	var stdout, stderr bytes.Buffer

	exitCode := runWithDeps([]string{"mcp", "remove", "docs", "--json"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})

	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	var payload struct {
		ServerName string `json:"serverName"`
		Removed    bool   `json:"removed"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if payload.ServerName != "docs" || !payload.Removed {
		t.Fatalf("payload = %#v, want docs removed", payload)
	}
	cfg := readMCPCommandConfig(t, configPath)
	if _, ok := cfg.MCP.Servers["docs"]; ok {
		t.Fatalf("docs server still present: %#v", cfg.MCP.Servers)
	}
	if _, ok := cfg.MCP.Servers["other"]; !ok {
		t.Fatalf("remove dropped unrelated server: %#v", cfg.MCP.Servers)
	}
}

func TestRunMCPToggleEnableDisablePreservesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	writeMCPCommandRawConfig(t, configPath, `{
  "activeProvider": "fast",
  "mcp": {
    "futureMCP": "keep",
    "servers": {
      "docs": {
        "type": "stdio",
        "command": "docs-mcp",
        "futureServer": "keep"
      }
    }
  }
}
`)

	var disableOut, disableErr bytes.Buffer
	disableCode := runWithDeps([]string{"mcp", "disable", "docs", "--json"}, &disableOut, &disableErr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if disableCode != exitSuccess {
		t.Fatalf("disable exitCode = %d stderr=%s", disableCode, disableErr.String())
	}
	var disablePayload struct {
		ServerName string `json:"serverName"`
		Disabled   bool   `json:"disabled"`
		Changed    bool   `json:"changed"`
	}
	if err := json.Unmarshal(disableOut.Bytes(), &disablePayload); err != nil {
		t.Fatalf("decode disable JSON: %v\n%s", err, disableOut.String())
	}
	if disablePayload.ServerName != "docs" || !disablePayload.Disabled || !disablePayload.Changed {
		t.Fatalf("disable payload = %#v, want docs disabled changed", disablePayload)
	}
	cfg := readMCPCommandConfig(t, configPath)
	if !cfg.MCP.Servers["docs"].Disabled {
		t.Fatalf("server not disabled: %#v", cfg.MCP.Servers["docs"])
	}

	var enableOut, enableErr bytes.Buffer
	enableCode := runWithDeps([]string{"mcp", "enable", "docs", "--json"}, &enableOut, &enableErr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if enableCode != exitSuccess {
		t.Fatalf("enable exitCode = %d stderr=%s", enableCode, enableErr.String())
	}
	var enablePayload struct {
		ServerName string `json:"serverName"`
		Disabled   bool   `json:"disabled"`
		Changed    bool   `json:"changed"`
	}
	if err := json.Unmarshal(enableOut.Bytes(), &enablePayload); err != nil {
		t.Fatalf("decode enable JSON: %v\n%s", err, enableOut.String())
	}
	if enablePayload.ServerName != "docs" || enablePayload.Disabled || !enablePayload.Changed {
		t.Fatalf("enable payload = %#v, want docs enabled changed", enablePayload)
	}

	raw := readMCPCommandRawConfig(t, configPath)
	mcpRaw := rawMCPCommandObject(t, raw["mcp"])
	if _, ok := mcpRaw["futureMCP"]; !ok {
		t.Fatalf("mcp unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, mcpRaw))
	}
	serversRaw := rawMCPCommandObject(t, mcpRaw["servers"])
	docsRaw := rawMCPCommandObject(t, serversRaw["docs"])
	if _, ok := docsRaw["futureServer"]; !ok {
		t.Fatalf("server unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, docsRaw))
	}
	if _, ok := docsRaw["disabled"]; ok {
		t.Fatalf("enable should remove disabled=false noise: %s", mustMarshalMCPCommandJSON(t, docsRaw))
	}
}

func TestRunMCPRemovePreservesUnrelatedConfigFields(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	writeMCPCommandRawConfig(t, configPath, `{
  "activeProvider": "fast",
  "futureTop": {"keep": true},
  "mcp": {
    "futureMCP": "keep",
    "servers": {
      "docs": {"type": "stdio", "command": "docs-mcp"},
      "other": {
        "type": "http",
        "url": "https://other.example/mcp",
        "futureServer": "keep"
      }
    }
  }
}
`)
	var stdout, stderr bytes.Buffer

	exitCode := runWithDeps([]string{"mcp", "remove", "docs"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})

	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	cfg := readMCPCommandConfig(t, configPath)
	if _, ok := cfg.MCP.Servers["docs"]; ok {
		t.Fatalf("docs server still present: %#v", cfg.MCP.Servers)
	}
	raw := readMCPCommandRawConfig(t, configPath)
	if _, ok := raw["futureTop"]; !ok {
		t.Fatalf("top-level unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, raw))
	}
	mcpRaw := rawMCPCommandObject(t, raw["mcp"])
	if _, ok := mcpRaw["futureMCP"]; !ok {
		t.Fatalf("mcp unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, mcpRaw))
	}
	serversRaw := rawMCPCommandObject(t, mcpRaw["servers"])
	if _, ok := serversRaw["docs"]; ok {
		t.Fatalf("docs server raw JSON still present: %s", mustMarshalMCPCommandJSON(t, serversRaw))
	}
	otherRaw := rawMCPCommandObject(t, serversRaw["other"])
	if _, ok := otherRaw["futureServer"]; !ok {
		t.Fatalf("unrelated server unknown field was not preserved: %s", mustMarshalMCPCommandJSON(t, otherRaw))
	}
}

func TestRunMCPCheckRegistersOnlyRequestedServer(t *testing.T) {
	cwd := t.TempDir()
	var registered config.MCPConfig
	closed := false
	var stdout, stderr bytes.Buffer

	exitCode := runWithDeps([]string{"mcp", "check", "docs", "--json"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			return config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"docs":  {Type: "stdio", Command: "docs-mcp"},
				"other": {Type: "stdio", Command: "other-mcp"},
			}}, nil
		},
		newMCPStore: func() (*mcp.PermissionStore, error) {
			return mcp.NewPermissionStore(mcp.StoreOptions{FilePath: filepath.Join(t.TempDir(), "permissions.json")})
		},
		registerMCPTools: func(ctx context.Context, registry *tools.Registry, cfg config.MCPConfig, options mcp.RegisterOptions) (mcpToolRuntime, error) {
			registered = cfg
			registry.Register(cliFakeMCPRegistryTool{})
			return closeFunc(func() error {
				closed = true
				return nil
			}), nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if _, ok := registered.Servers["docs"]; !ok || len(registered.Servers) != 1 {
		t.Fatalf("registered cfg = %#v, want only docs", registered.Servers)
	}
	if !closed {
		t.Fatal("MCP runtime was not closed")
	}
	var payload struct {
		ServerName string            `json:"serverName"`
		Status     string            `json:"status"`
		ToolCount  int               `json:"toolCount"`
		Tools      []mcpToolListItem `json:"tools"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if payload.ServerName != "docs" || payload.Status != "ok" || payload.ToolCount != 1 || len(payload.Tools) != 1 {
		t.Fatalf("payload = %#v, want docs ok with one tool", payload)
	}
}

func TestRunMCPCheckClosesRuntimeReturnedWithError(t *testing.T) {
	cwd := t.TempDir()
	closed := false
	var stdout, stderr bytes.Buffer

	exitCode := runWithDeps([]string{"mcp", "check", "docs"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			return config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"docs": {Type: "stdio", Command: "docs-mcp"},
			}}, nil
		},
		newMCPStore: func() (*mcp.PermissionStore, error) {
			return mcp.NewPermissionStore(mcp.StoreOptions{FilePath: filepath.Join(t.TempDir(), "permissions.json")})
		},
		registerMCPTools: func(ctx context.Context, registry *tools.Registry, cfg config.MCPConfig, options mcp.RegisterOptions) (mcpToolRuntime, error) {
			return closeFunc(func() error {
				closed = true
				return nil
			}), os.ErrPermission
		},
	})

	if exitCode != exitCrash {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !closed {
		t.Fatal("MCP runtime returned with error was not closed")
	}
}

func TestRunMCPConfigCommandsReportCommandSpecificUnknownFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "remove", args: []string{"mcp", "remove", "docs", "--bogus"}, want: `unknown mcp remove flag "--bogus"`},
		{name: "check", args: []string{"mcp", "check", "docs", "--bogus"}, want: `unknown mcp check flag "--bogus"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			exitCode := runWithDeps(tc.args, &stdout, &stderr, appDeps{})

			if exitCode != exitUsage {
				t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr missing %q:\n%s", tc.want, stderr.String())
			}
			if strings.Contains(stderr.String(), "permissions") {
				t.Fatalf("stderr referenced permissions parser:\n%s", stderr.String())
			}
		})
	}
}

func TestRunMCPHelpMentionsConfigCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer

	exitCode := runWithDeps([]string{"mcp", "--help"}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	for _, want := range []string{"add <server>", "remove <server>", "enable <server>", "disable <server>", "check <server>", "list", "oauth", "permissions", "tools"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, stdout.String())
		}
	}
}

func writeMCPCommandRawConfig(t *testing.T, path string, data string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeMCPCommandConfig(t *testing.T, path string, cfg config.FileConfig) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func readMCPCommandConfig(t *testing.T, path string) config.FileConfig {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v\n%s", err, string(data))
	}
	return cfg
}

func readMCPCommandRawConfig(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw config: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode raw config: %v\n%s", err, string(data))
	}
	return raw
}

func rawMCPCommandObject(t *testing.T, data json.RawMessage) map[string]json.RawMessage {
	t.Helper()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode raw object: %v\n%s", err, string(data))
	}
	return raw
}

func mustMarshalMCPCommandJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(data)
}
