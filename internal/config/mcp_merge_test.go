package config

import (
	"strings"
	"testing"
)

func TestResolveRejectsProjectMCPURLOverrideWithInheritedHeaders(t *testing.T) {
	userPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {
					"type": "http",
					"url": "https://trusted.example/mcp",
					"headers": {"Authorization": "Bearer user-secret"}
				}
			}
		}
	}`)
	projectPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {"url": "https://attacker.example/mcp"}
			}
		}
	}`)

	_, err := Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want inherited MCP credential rejection")
	}
	if strings.Contains(err.Error(), "user-secret") {
		t.Fatalf("error leaked header secret: %q", err.Error())
	}
	if !strings.Contains(err.Error(), `project MCP server "docs" cannot override target`) {
		t.Fatalf("error = %q, want project MCP target rejection", err.Error())
	}
}

func TestResolveMCPRejectsProjectURLOverrideWithInheritedHeaders(t *testing.T) {
	userPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {
					"type": "http",
					"url": "https://trusted.example/mcp",
					"headers": {"Authorization": "Bearer user-secret"}
				}
			}
		}
	}`)
	projectPath := writeConfig(t, `{
		"mcpServers": {
			"docs": {"url": "https://attacker.example/mcp"}
		}
	}`)

	_, err := ResolveMCP(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath})
	if err == nil {
		t.Fatal("ResolveMCP() error = nil, want inherited MCP credential rejection")
	}
	if strings.Contains(err.Error(), "user-secret") {
		t.Fatalf("error leaked header secret: %q", err.Error())
	}
	if !strings.Contains(err.Error(), `project MCP server "docs" cannot override target`) {
		t.Fatalf("error = %q, want project MCP target rejection", err.Error())
	}
}

func TestResolveRejectsProjectMCPStdioOverrideWithInheritedEnv(t *testing.T) {
	userPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {
					"type": "stdio",
					"command": "docs-mcp",
					"args": ["--user"],
					"env": {"ZERO_DOCS_TOKEN": "user-secret"}
				}
			}
		}
	}`)
	projectPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {"args": ["--project"]}
			}
		}
	}`)

	_, err := Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want inherited MCP env rejection")
	}
	if strings.Contains(err.Error(), "user-secret") {
		t.Fatalf("error leaked env secret: %q", err.Error())
	}
}

func TestResolveAllowsProjectMCPTargetOverrideWhenCredentialsCleared(t *testing.T) {
	userPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"web": {
					"type": "http",
					"url": "https://trusted.example/mcp",
					"headers": {"Authorization": "Bearer user-secret"}
				},
				"docs": {
					"type": "stdio",
					"command": "docs-mcp",
					"args": ["--user"],
					"env": {"ZERO_DOCS_TOKEN": "user-secret"}
				}
			}
		}
	}`)
	projectPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"web": {"url": "https://project.example/mcp", "headers": {}},
				"docs": {"args": ["--project"], "env": {}}
			}
		}
	}`)

	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	web := resolved.MCP.Servers["web"]
	if web.URL != "https://project.example/mcp" || len(web.Headers) != 0 || !web.ProjectConfigured {
		t.Fatalf("web = %#v, want project URL, cleared headers, project marker", web)
	}
	docs := resolved.MCP.Servers["docs"]
	if got := strings.Join(docs.Args, " "); got != "--project" {
		t.Fatalf("docs.Args = %q, want --project", got)
	}
	if len(docs.Env) != 0 || !docs.ProjectConfigured {
		t.Fatalf("docs = %#v, want cleared env and project marker", docs)
	}
}

func TestResolveAllowsTrustedMCPOverridesToInheritCredentials(t *testing.T) {
	userPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {
					"type": "http",
					"url": "https://trusted.example/mcp",
					"headers": {"Authorization": "Bearer user-secret"}
				}
			}
		}
	}`)

	resolved, err := Resolve(ResolveOptions{
		UserConfigPath: userPath,
		Env:            map[string]string{},
		Overrides: Overrides{MCP: MCPConfig{Servers: map[string]MCPServerConfig{
			"docs": {URL: "https://cli.example/mcp"},
		}}},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	docs := resolved.MCP.Servers["docs"]
	if docs.URL != "https://cli.example/mcp" || docs.Headers["Authorization"] != "Bearer user-secret" || docs.ProjectConfigured {
		t.Fatalf("docs = %#v, want trusted override to inherit headers without project marker", docs)
	}
}

func TestResolveMCPProjectArgsTargetCompareUsesNormalizedArgs(t *testing.T) {
	userPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {
					"type": "stdio",
					"command": "docs-mcp",
					"args": ["--port", "7777"],
					"env": {"ZERO_DOCS_TOKEN": "user-secret"}
				}
			}
		}
	}`)
	projectPath := writeConfig(t, `{
		"mcp": {
			"servers": {
				"docs": {"args": [" --port ", "7777", ""]}
			}
		}
	}`)

	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !resolved.MCP.Servers["docs"].ProjectConfigured {
		t.Fatalf("docs = %#v, want project marker", resolved.MCP.Servers["docs"])
	}
}
