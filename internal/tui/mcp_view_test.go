package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestMCPViewRendersEmptyState(t *testing.T) {
	got := plainRender(t, renderMCPView(MCPViewState{}, 72))

	for _, want := range []string{
		"MCP",
		"status: warning",
		"No MCP servers configured.",
		"zero mcp list",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("empty MCP view = %q, missing %q", got, want)
		}
	}
}

func TestMCPViewRendersServerRows(t *testing.T) {
	got := plainRender(t, renderMCPView(MCPViewState{
		Servers: []MCPServerView{
			{Name: "filesystem", Transport: "stdio", State: "connected", Target: "npx @modelcontextprotocol/server-filesystem /repo", ToolCount: 3},
			{Name: "linear", Transport: "http", State: "disabled", Target: "https://mcp.linear.app", Auth: "oauth"},
		},
	}, 96))

	for _, want := range []string{
		"Servers",
		"filesystem [stdio] connected",
		"3 tools",
		"linear [http] disabled oauth",
		"https://mcp.linear.app",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MCP server view = %q, missing %q", got, want)
		}
	}
}

func TestMCPViewRendersToolRows(t *testing.T) {
	got := plainRender(t, renderMCPView(MCPViewState{
		Tools: []MCPToolView{
			{ServerName: "filesystem", Name: "read_file", RegistryName: "mcp_filesystem_read_file", SideEffect: "read", Permission: "allow", Description: "Read a file from the workspace"},
			{ServerName: "github", Name: "create_issue", RegistryName: "mcp_github_create_issue", SideEffect: "network", Permission: "prompt", Description: "Create an issue in GitHub"},
		},
	}, 140))

	for _, want := range []string{
		"Tools",
		"mcp_filesystem_read_file [read/allow]",
		"filesystem/read_file",
		"mcp_github_create_issue [network/prompt]",
		"Create an issue in GitHub",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MCP tools view = %q, missing %q", got, want)
		}
	}
}

func TestMCPViewRendersPermissionStates(t *testing.T) {
	got := plainRender(t, renderMCPView(MCPViewState{
		Permissions: MCPPermissionSummary{
			Mode:         "ask",
			GrantCount:   2,
			ServerGrants: 1,
			ToolGrants:   1,
			PromptCount:  4,
			DeniedCount:  1,
			Grants: []MCPPermissionGrantView{
				{Target: "filesystem/*", Autonomy: "low", ApprovedAt: "2026-06-13T09:30:00Z"},
				{Target: "github/create_issue", Autonomy: "medium", ApprovedAt: "2026-06-13T10:00:00Z"},
			},
		},
	}, 92))

	for _, want := range []string{
		"Permissions",
		"mode: ask",
		"persistent grants: 2",
		"server grants: 1",
		"tool grants: 1",
		"prompted this session: 4",
		"denied this session: 1",
		"filesystem/* [low] approved 2026-06-13T09:30:00Z",
		"github/create_issue [medium] approved 2026-06-13T10:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MCP permission view = %q, missing %q", got, want)
		}
	}
}

func TestMCPViewRendersOAuthSummary(t *testing.T) {
	expiry := time.Date(2026, 6, 13, 11, 45, 0, 0, time.UTC)
	got := plainRender(t, renderMCPView(MCPViewState{
		OAuth: MCPOAuthSummary{
			Servers: []MCPOAuthServerView{
				{ServerName: "linear", Configured: true, HasToken: true, HasRefreshToken: true, TokenType: "Bearer", Scopes: []string{"issues:read", "issues:write"}, ExpiresAt: expiry},
				{ServerName: "notion", Configured: true, HasToken: true, Expired: true},
				{ServerName: "plain", Configured: false},
			},
		},
	}, 160))

	for _, want := range []string{
		"OAuth",
		"linear configured token refresh expires 2026-06-13T11:45:00Z Bearer scopes issues:read,issues:write",
		"notion configured token expired",
		"plain not configured",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MCP OAuth view = %q, missing %q", got, want)
		}
	}
}

func TestMCPViewNeverExceedsWidth(t *testing.T) {
	state := MCPViewState{
		Servers: []MCPServerView{{
			Name:      "very-long-server-name",
			Transport: "stdio",
			State:     "connected",
			Target:    "C:/Users/example/workspaces/very/deep/path/with/a/long/command --and --many --arguments",
			ToolCount: 12,
		}},
		Tools: []MCPToolView{{
			ServerName:   "very-long-server-name",
			Name:         "tool_with_an_extremely_long_name",
			RegistryName: "mcp_very_long_server_name_tool_with_an_extremely_long_name",
			SideEffect:   "network",
			Permission:   "prompt",
			Description:  "This description is intentionally long so the renderer has to trim it safely.",
		}},
		Permissions: MCPPermissionSummary{
			Mode:       "ask",
			GrantCount: 1,
			Grants: []MCPPermissionGrantView{{
				Target:     "very-long-server-name/tool_with_an_extremely_long_name",
				Autonomy:   "medium",
				ApprovedAt: "2026-06-13T10:00:00Z",
			}},
		},
		OAuth: MCPOAuthSummary{Servers: []MCPOAuthServerView{{
			ServerName:      "very-long-server-name",
			Configured:      true,
			HasToken:        true,
			HasRefreshToken: true,
			TokenType:       "Bearer",
			Scopes:          []string{"workspace:read", "workspace:write", "offline_access"},
			ExpiresAt:       time.Date(2026, 6, 13, 11, 45, 0, 0, time.UTC),
		}}},
	}

	for _, width := range []int{24, 40, 58, 72, 96} {
		for index, line := range strings.Split(renderMCPView(state, width), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: line %d is %d cells wide: %q", width, index, got, line)
			}
		}
	}
}
