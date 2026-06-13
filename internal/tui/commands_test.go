package tui

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	internalmcp "github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestFormatCommandHelpLinesGroupsCommandsByStableOrder(t *testing.T) {
	lines := formatCommandHelpLines()
	help := strings.Join(lines, "\n")

	groupOrder := []string{"model:", "session:", "runtime:", "tools:", "meta:"}
	lastIndex := -1
	for _, group := range groupOrder {
		index := strings.Index(help, group)
		if index < 0 {
			t.Fatalf("expected grouped help to contain %q, got:\n%s", group, help)
		}
		if index <= lastIndex {
			t.Fatalf("expected %q after previous groups, got:\n%s", group, help)
		}
		lastIndex = index
	}

	for _, want := range []string{
		"model:",
		"  /provider [status] - Open provider setup.",
		"  /model [list|id] - Show or switch the active model.",
		"  /effort [list|low|medium|high|auto] - Show or set reasoning effort for supported models.",
		"session:",
		"  /plan - Show planning mode status.",
		"runtime:",
		"  /permissions - Show the active permission mode and sandbox grants.",
		"  /debug (/debug-mode) - Show debug mode status.",
		"tools:",
		"  /mcp (/mcp-status) - Show MCP server status.",
		"  /search <query> (/find) - Search local session events. Requires a query argument.",
		"meta:",
		"  /exit (/quit) - Exit Zero.",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected grouped help to contain %q, got:\n%s", want, help)
		}
	}
}

func TestMCPCommandMetadataAndAutocomplete(t *testing.T) {
	command, ok := resolveCommand("/mcp")
	if !ok {
		t.Fatal("expected /mcp to resolve")
	}
	if command.kind == commandUnknown || command.kind == commandPrompt || command.kind == commandEmpty {
		t.Fatalf("expected /mcp to resolve to a concrete command kind, got %v", command.kind)
	}
	if command.group != commandGroupTools {
		t.Fatalf("expected /mcp in tools group, got %q", command.group)
	}
	if commandSelectionRequiresInput("/mcp") {
		t.Fatal("/mcp should run without required input")
	}

	alias, ok := resolveCommand("/mcp-status")
	if !ok || alias.kind != command.kind {
		t.Fatalf("expected /mcp-status to resolve to MCP command, got ok=%v command=%#v", ok, alias)
	}

	names := listCommandNames()
	for _, want := range []string{"/mcp", "/mcp-status"} {
		if !commandTestStringSliceContains(names, want) {
			t.Fatalf("expected command names to contain %s, got %#v", want, names)
		}
	}

	for _, token := range []string{"/mc", "/mcp-status"} {
		if !commandSuggestionNamesContain(matchCommandSuggestions(token), "/mcp") {
			t.Fatalf("expected autocomplete for %q to surface canonical /mcp", token)
		}
	}
}

func TestMCPCommandRendersConfiguredStateWithoutAgentRun(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(commandTestMCPTool{
		name:        "mcp_docs_lookup",
		serverName:  "docs",
		description: "Look up docs",
		safety: tools.Safety{
			SideEffect: tools.SideEffectNetwork,
			Permission: tools.PermissionPrompt,
		},
	})

	permissionStore, err := internalmcp.NewPermissionStore(internalmcp.StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "mcp-permissions.json"),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 9, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewPermissionStore returned error: %v", err)
	}
	if _, err := permissionStore.GrantServer(internalmcp.GrantServerInput{
		ServerName:     "docs",
		ServerIdentity: "docs-identity",
		MaxAutonomy:    internalmcp.AutonomyLow,
	}); err != nil {
		t.Fatalf("GrantServer returned error: %v", err)
	}
	if _, err := permissionStore.GrantTool(internalmcp.GrantToolInput{
		ServerName:     "github",
		ServerIdentity: "github-identity",
		ToolName:       "create_issue",
		MaxAutonomy:    internalmcp.AutonomyMedium,
	}); err != nil {
		t.Fatalf("GrantTool returned error: %v", err)
	}

	tokenStore, err := internalmcp.NewTokenStore(internalmcp.TokenStoreOptions{
		FilePath: filepath.Join(t.TempDir(), "mcp-oauth.json"),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewTokenStore returned error: %v", err)
	}
	if err := tokenStore.Save("github", internalmcp.StoredToken{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Scopes:       []string{"issues:read", "issues:write"},
		ExpiresAt:    time.Date(2026, 6, 13, 11, 45, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Save token returned error: %v", err)
	}

	m := newModel(context.Background(), Options{
		Registry:       registry,
		PermissionMode: agent.PermissionModeAsk,
	})
	m.mcpConfig = config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {
			Type:    "stdio",
			Command: "zero-docs-mcp",
			Args:    []string{"--workspace", "."},
		},
		"github": {
			Type: "http",
			URL:  "https://mcp.github.example",
			Auth: "oauth",
			OAuth: &config.MCPOAuthConfig{
				Scopes: []string{"issues:read", "issues:write"},
			},
		},
	}}
	m.mcpPermissionStore = permissionStore
	m.mcpTokenStore = tokenStore
	m.width = 220
	m.height = 42
	m.input.SetValue("/mcp")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /mcp to be handled without starting an agent run")
	}
	if next.pending || next.activeRunID != 0 || next.runID != 0 {
		t.Fatalf("expected /mcp not to mutate agent run state, pending=%v activeRunID=%d runID=%d", next.pending, next.activeRunID, next.runID)
	}
	if next.mcpManager == nil {
		t.Fatal("expected /mcp to open the selectable MCP manager")
	}
	if len(next.transcript) != len(m.transcript) {
		t.Fatalf("/mcp should open a manager overlay without appending transcript rows; before=%d after=%d", len(m.transcript), len(next.transcript))
	}
	text := plainRender(t, next.View())
	for _, want := range []string{
		"Manage MCP servers",
		"2 servers",
		"User MCPs",
		"docs",
		"enabled",
		"github",
		"oauth",
		"Add MCP server",
		"Add local stdio MCP",
		"List configured",
		"d disable",
		"r remove",
		"Enter action",
		"Esc close",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected clean MCP manager overlay to contain %q, got:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{
		"lookup [network/prompt]",
		"persistent grants:",
		"server grants:",
		"OAuth",
		"add: zero mcp add",
		"disconnect: zero mcp disable",
	} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("MCP manager overlay should not include old status report text %q:\n%s", unwanted, text)
		}
	}
}

func TestMCPManagerNavigationPrefillsAddCommand(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/mcp")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /mcp to open synchronously")
	}
	if next.mcpManager == nil {
		t.Fatal("expected MCP manager to open")
	}

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected MCP manager selection to prefill synchronously")
	}
	if next.mcpManager != nil {
		t.Fatal("expected add command selection to close the MCP manager")
	}
	if got, want := next.input.Value(), "/mcp add <name> --url <url>"; got != want {
		t.Fatalf("composer = %q, want %q", got, want)
	}
}

func TestMCPManagerRunsSelectedServerAction(t *testing.T) {
	var called []string
	m := newModel(context.Background(), Options{
		PermissionMode: agent.PermissionModeAsk,
		MCPConfig: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"docs": {Type: "stdio", Command: "zero-docs-mcp"},
		}},
		MCPCommand: func(args []string) MCPCommandResult {
			called = append([]string{}, args...)
			return MCPCommandResult{
				ExitCode: 0,
				Output:   "MCP server docs is now disabled.",
				Config: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
					"docs": {Type: "stdio", Command: "zero-docs-mcp", Disabled: true},
				}},
			}
		},
	})
	m.input.SetValue("/mcp")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.mcpManager == nil {
		t.Fatal("expected MCP manager to open")
	}

	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected MCP action to run synchronously")
	}
	if !reflect.DeepEqual(called, []string{"disable", "docs"}) {
		t.Fatalf("MCPCommand args = %#v, want disable docs", called)
	}
	if !next.mcpConfig.Servers["docs"].Disabled {
		t.Fatalf("docs server was not disabled in TUI state: %#v", next.mcpConfig.Servers["docs"])
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{
		"MCP action complete",
		"MCP server docs is now disabled.",
		"docs",
		"disabled",
		"stdio",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("MCP manager action output missing %q:\n%s", want, text)
		}
	}
}

func TestMCPCommandRunsManagerActionAndRefreshesState(t *testing.T) {
	m := newModel(context.Background(), Options{
		PermissionMode: agent.PermissionModeAsk,
		MCPConfig: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"docs": {Type: "stdio", Command: "zero-docs-mcp"},
		}},
		MCPCommand: func(args []string) MCPCommandResult {
			if !reflect.DeepEqual(args, []string{"disable", "docs"}) {
				t.Fatalf("MCPCommand args = %#v, want disable docs", args)
			}
			return MCPCommandResult{
				ExitCode: 0,
				Output:   "MCP server docs is now disabled.",
				Config: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
					"docs": {Type: "stdio", Command: "zero-docs-mcp", Disabled: true},
				}},
			}
		},
	})
	m.input.SetValue("/mcp disable docs")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /mcp disable to run synchronously")
	}
	if !next.mcpConfig.Servers["docs"].Disabled {
		t.Fatalf("docs server was not disabled in TUI state: %#v", next.mcpConfig.Servers["docs"])
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{
		"MCP action complete",
		"MCP server docs is now disabled.",
		"docs · disabled · stdio",
		"zero mcp enable docs",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("/mcp action text missing %q:\n%s", want, text)
		}
	}
}

type commandTestMCPTool struct {
	name        string
	serverName  string
	description string
	safety      tools.Safety
}

func (tool commandTestMCPTool) Name() string {
	return tool.name
}

func (tool commandTestMCPTool) Description() string {
	return tool.description
}

func (tool commandTestMCPTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object"}
}

func (tool commandTestMCPTool) Safety() tools.Safety {
	return tool.safety
}

func (tool commandTestMCPTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}

func (tool commandTestMCPTool) MCPServerName() string {
	return tool.serverName
}

func commandTestStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func commandSuggestionNamesContain(suggestions []commandSuggestion, want string) bool {
	for _, suggestion := range suggestions {
		if suggestion.Name == want {
			return true
		}
	}
	return false
}

func TestParseImageCommand(t *testing.T) {
	cases := []struct {
		input string
		kind  commandKind
		text  string
	}{
		{input: "/image photo.png", kind: commandImage, text: "photo.png"},
		{input: "/image ./a b.png", kind: commandImage, text: "./a b.png"},
		{input: "/image clear", kind: commandImage, text: "clear"},
		{input: "/image", kind: commandImage, text: ""},
	}
	for _, tc := range cases {
		got := parseCommand(tc.input)
		if got.kind != tc.kind || got.text != tc.text {
			t.Fatalf("%q: got kind=%v text=%q, want kind=%v text=%q", tc.input, got.kind, got.text, tc.kind, tc.text)
		}
	}
}

func TestCommandSelectionRequiresInputFromUsage(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "/spec", want: true},
		{name: "/search", want: true},
		{name: "/find", want: true},
		{name: "/image", want: true},
		{name: "/rewind", want: false},
		{name: "/model", want: false},
		{name: "/help", want: false},
	}
	for _, tc := range cases {
		if got := commandSelectionRequiresInput(tc.name); got != tc.want {
			t.Fatalf("commandSelectionRequiresInput(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCommandRequiredInputHintFromUsage(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "/spec", want: "[task]"},
		{name: "/search", want: "[query]"},
		{name: "/find", want: "[query]"},
		{name: "/image", want: "[path]"},
		{name: "/model", want: ""},
	}
	for _, tc := range cases {
		if got := commandRequiredInputHint(tc.name); got != tc.want {
			t.Fatalf("commandRequiredInputHint(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestImageCommandIsDiscoverable(t *testing.T) {
	found := false
	for _, name := range listCommandNames() {
		if name == "/image" {
			found = true
		}
	}
	if !found {
		t.Fatal("/image should be listed so it appears in help and autocomplete")
	}
}
