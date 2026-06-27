package tools

import (
	"context"
	"strings"
	"testing"
)

func TestShortenDescription(t *testing.T) {
	tests := []struct {
		name string
		desc string
		max  int
		want string
	}{
		{name: "empty", desc: "", max: 200, want: ""},
		{name: "single short line", desc: "Reads a file from disk.", max: 200, want: "Reads a file from disk."},
		{
			name: "takes first non-empty line",
			desc: "\n\nReads a file.\nSecond line ignored.",
			max:  200,
			want: "Reads a file.",
		},
		{
			name: "strips markdown heading",
			desc: "## Read tool\nbody",
			max:  200,
			want: "Read tool",
		},
		{
			name: "generic heading joins next line",
			desc: "Overview\nFetches a URL over HTTP.",
			max:  200,
			want: "Overview — Fetches a URL over HTTP.",
		},
		{
			name: "collapses internal whitespace",
			desc: "Runs   a\tshell    command",
			max:  200,
			want: "Runs a shell command",
		},
		{
			name: "truncates on word boundary with ellipsis",
			desc: "alpha beta gamma delta epsilon zeta",
			max:  20,
			want: "alpha beta gamma…",
		},
		{
			name: "truncates mid-word when no boundary",
			desc: "supercalifragilistic",
			max:  10,
			want: "supercali…",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shortenDescription(tc.desc, tc.max)
			if got != tc.want {
				t.Fatalf("shortenDescription(%q, %d) = %q, want %q", tc.desc, tc.max, got, tc.want)
			}
			if len([]rune(got)) > tc.max && tc.max > 0 {
				t.Fatalf("result %q exceeds max %d runes", got, tc.max)
			}
			if strings.Contains(got, "\n") {
				t.Fatalf("result %q contains a newline", got)
			}
		})
	}
}

func TestFormatInputSchemaHint(t *testing.T) {
	tests := []struct {
		name   string
		schema Schema
		want   string
	}{
		{
			name:   "no properties",
			schema: Schema{Type: "object"},
			want:   "(none)",
		},
		{
			name: "required marked and sorted",
			schema: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"a": {Type: "string"},
					"b": {Type: "number"},
				},
				Required: []string{"a"},
			},
			want: "inputs (* required): a (string)*, b (number)",
		},
		{
			name: "untyped property omits type parens",
			schema: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"q": {},
				},
				Required: []string{"q"},
			},
			want: "inputs (* required): q*",
		},
		{
			name: "caps at four and reports remainder",
			schema: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"a": {Type: "string"},
					"b": {Type: "string"},
					"c": {Type: "string"},
					"d": {Type: "string"},
					"e": {Type: "string"},
					"f": {Type: "string"},
				},
				Required: []string{"a"},
			},
			want: "inputs (* required): a (string)*, b (string), c (string), d (string); +2 more",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatInputSchemaHint(tc.schema); got != tc.want {
				t.Fatalf("formatInputSchemaHint() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMCPServerFromToolName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     string
	}{
		{name: "standard mcp name", toolName: "mcp_github_create_issue", want: "github"},
		{name: "single tool segment", toolName: "mcp_weather_forecast", want: "weather"},
		{name: "server with no tool part", toolName: "mcp_github_tool", want: "github"},
		{name: "non-mcp builtin", toolName: "read_file", want: ""},
		{name: "tool_search itself", toolName: "tool_search", want: ""},
		{name: "prefix only no server", toolName: "mcp_", want: ""},
		{name: "prefix and server but no tool", toolName: "mcp_github", want: ""},
		{name: "empty", toolName: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpServerFromToolName(tc.toolName); got != tc.want {
				t.Fatalf("mcpServerFromToolName(%q) = %q, want %q", tc.toolName, got, tc.want)
			}
		})
	}
}

func TestFormatDeferredToolLine(t *testing.T) {
	schema := Schema{
		Type: "object",
		Properties: map[string]PropertySchema{
			"url": {Type: "string"},
		},
		Required: []string{"url"},
	}

	t.Run("with server", func(t *testing.T) {
		got := formatDeferredToolLine("mcp_github_fetch", "Fetches a URL.", "github", schema)
		want := "mcp_github_fetch: Fetches a URL. | server: github | inputs (* required): url (string)*"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("omits server when empty", func(t *testing.T) {
		got := formatDeferredToolLine("read_file", "Reads a file.", "", schema)
		want := "read_file: Reads a file. | inputs (* required): url (string)*"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if strings.Contains(got, "server:") {
			t.Fatalf("server part leaked into %q", got)
		}
	})

	t.Run("empty schema shows none", func(t *testing.T) {
		got := formatDeferredToolLine("ping", "Pings.", "", Schema{Type: "object"})
		want := "ping: Pings. | (none)"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("blank description gets placeholder", func(t *testing.T) {
		got := formatDeferredToolLine("noop", "", "", Schema{Type: "object"})
		want := "noop: No description provided | (none)"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("long description truncated", func(t *testing.T) {
		long := strings.Repeat("word ", 80) // 400 chars
		got := formatDeferredToolLine("big", long, "", Schema{Type: "object"})
		if !strings.Contains(got, "…") {
			t.Fatalf("expected truncation ellipsis in %q", got)
		}
		descPart := strings.SplitN(strings.TrimPrefix(got, "big: "), " | ", 2)[0]
		if len([]rune(descPart)) > defaultShortenMax {
			t.Fatalf("description part %d runes exceeds %d", len([]rune(descPart)), defaultShortenMax)
		}
	})
}

func TestBuildToolSearchDescription(t *testing.T) {
	t.Run("no sources still describes discovery", func(t *testing.T) {
		got := BuildToolSearchDescription(nil)
		for _, want := range []string{"# Tool discovery", "No deferred tools are currently hidden.", "tool_search"} {
			if !strings.Contains(got, want) {
				t.Fatalf("BuildToolSearchDescription(nil) = %q, missing %q", got, want)
			}
		}
	})

	t.Run("lists compact tool names and sources", func(t *testing.T) {
		tools := []Tool{
			serverNamedTool{
				baseTool: baseTool{name: "mcp_git_hub_create_issue", description: "Create an issue.", parameters: Schema{Type: "object"}},
				server:   "git hub",
			},
			fakeDeferredTool{baseTool: baseTool{name: "swarm_spawn", description: "Spawn a worker.", parameters: Schema{Type: "object"}}, deferred: true},
		}
		got := BuildToolSearchDescription(tools)

		for _, want := range []string{
			"# Tool discovery",
			"- mcp_git_hub_create_issue — git hub",
			"- swarm_spawn — swarm",
			"select:Name1,Name2",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("BuildToolSearchDescription = %q, missing %q", got, want)
			}
		}
		for _, avoid := range []string{"Create an issue", "Spawn a worker"} {
			if strings.Contains(got, avoid) {
				t.Fatalf("BuildToolSearchDescription should not list exact tool metadata %q in %q", avoid, got)
			}
		}
	})
}

func TestDeferredLineUsesToolFieldsAndServer(t *testing.T) {
	tool := fakeDeferredTool{
		baseTool: baseTool{
			name:        "mcp_docs_lookup",
			description: "Look up documentation for a symbol.",
			parameters: Schema{
				Type:       "object",
				Properties: map[string]PropertySchema{"symbol": {Type: "string"}},
				Required:   []string{"symbol"},
			},
		},
		deferred: true,
	}
	got := DeferredLine(tool)
	for _, want := range []string{"mcp_docs_lookup: Look up documentation", "server: docs", "inputs (* required): symbol (string)*"} {
		if !strings.Contains(got, want) {
			t.Fatalf("DeferredLine = %q, missing %q", got, want)
		}
	}
}

// serverNamedTool implements the optional mcpServerNamed interface so DeferredLine
// can prefer the reported server name over the name-derived token.
type serverNamedTool struct {
	baseTool
	server string
}

func (t serverNamedTool) Run(context.Context, map[string]any) Result { return okResult("ok") }
func (t serverNamedTool) Deferred() bool                             { return true }
func (t serverNamedTool) MCPServerName() string                      { return t.server }

func TestDeferredLinePrefersMCPServerName(t *testing.T) {
	// The tool name sanitizes to a server token containing an underscore
	// ("git_hub"): mcpServerFromToolName would truncate the label to "git", so the
	// reported server name must win.
	tool := serverNamedTool{
		baseTool: baseTool{
			name:        "mcp_git_hub_create_issue",
			description: "Create a GitHub issue.",
			parameters:  Schema{Type: "object"},
		},
		server: "git hub",
	}
	got := DeferredLine(tool)
	if !strings.Contains(got, "server: git hub") {
		t.Fatalf("DeferredLine = %q, want server label from MCPServerName() (%q)", got, "git hub")
	}
	if strings.Contains(got, "server: git |") {
		t.Fatalf("DeferredLine = %q, used truncated name-derived token instead of MCPServerName()", got)
	}
}

// A blank/whitespace MCPServerName() must fall back to the name-derived token so a
// tool that reports nothing useful is still labeled from its synthesized name.
func TestDeferredLineFallsBackWhenMCPServerNameBlank(t *testing.T) {
	tool := serverNamedTool{
		baseTool: baseTool{
			name:        "mcp_docs_lookup",
			description: "Look up docs.",
			parameters:  Schema{Type: "object"},
		},
		server: "  ",
	}
	got := DeferredLine(tool)
	if !strings.Contains(got, "server: docs") {
		t.Fatalf("DeferredLine = %q, want fallback server token %q", got, "docs")
	}
}

// TestMCPServerFromToolNameRoundTrip pins the fallback parser against the name
// format produced for a single-token MCP server ("mcp_<server>_<tool>"), the same
// format mcp.registryToolName synthesizes.
func TestMCPServerFromToolNameRoundTrip(t *testing.T) {
	cases := map[string]string{
		"mcp_docs_lookup":         "docs",
		"mcp_weather_forecast":    "weather",
		"mcp_github_create_issue": "github",
	}
	for name, wantServer := range cases {
		if got := mcpServerFromToolName(name); got != wantServer {
			t.Fatalf("mcpServerFromToolName(%q) = %q, want %q", name, got, wantServer)
		}
	}
}
