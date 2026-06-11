package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestHelpCommandRendersGroupedSections(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/help")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /help to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{
		"Commands",
		"Model",
		"Session",
		"Runtime",
		"Tools",
		"Meta",
		"  /model [list|id]",
		"  /permissions",
		"hint: submit plain text to ask Zero",
	} {
		assertContains(t, text, want)
	}
	assertNotContains(t, text, "Commands:\n/provider")
}

func TestProviderAndConfigCommandsUseStableStatusOutput(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			APIKey:       "sk-sensitive",
			Model:        "gpt-4.1",
		},
		AgentOptions: agent.Options{MaxTurns: 42},
	})

	m.input.SetValue("/provider status")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /provider to be handled without starting an agent run")
	}
	providerText := transcriptText(next.transcript)
	for _, want := range []string{"Provider", "status: ok", "provider: openai", "model: gpt-4.1", "api key: set"} {
		assertContains(t, providerText, want)
	}
	assertNotContains(t, providerText, "sk-sensitive")

	next.input.SetValue("/config")
	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected /config to be handled without starting an agent run")
	}
	configText := transcriptText(next.transcript)
	for _, want := range []string{"Config", "status: ok", "runtime: go", "max turns: 42", "permission mode:"} {
		assertContains(t, configText, want)
	}
	assertNotContains(t, configText, "sk-sensitive")
}

func TestProviderCommandRedactsCredentialBearingBaseURL(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      "https://user:super-secret@proxy.local/v1?api_key=query-secret",
			APIKey:       "query-secret",
			Model:        "gpt-4.1",
		},
	})
	m.input.SetValue("/provider status")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /provider to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, unwanted := range []string{"super-secret", "query-secret", "user:super-secret@"} {
		assertNotContains(t, text, unwanted)
	}
	assertContains(t, text, "base url: https://proxy.local/v1?api_key=[REDACTED]")
}

func TestContextAndPermissionsCommandsRenderProductState(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))

	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	if _, err := store.Grant(sandbox.GrantInput{
		ToolName:    "bash",
		Decision:    sandbox.GrantAllow,
		MaxAutonomy: sandbox.AutonomyHigh,
		Reason:      "sk-proj-sensitive approved shell",
	}); err != nil {
		t.Fatalf("Grant returned error: %v", err)
	}

	m := newModel(context.Background(), Options{
		Cwd:            `D:\codings\Opensource\Zero`,
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		Registry:       registry,
		SandboxStore:   store,
		PermissionMode: agent.PermissionModeAsk,
	})

	m.input.SetValue("/context")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /context to be handled without starting an agent run")
	}
	contextText := transcriptText(next.transcript)
	for _, want := range []string{"Context", "status: ok", "Runtime", "Session", "Tools", "cwd: D:\\codings\\Opensource\\Zero", "registered tools: 1"} {
		assertContains(t, contextText, want)
	}

	next.input.SetValue("/permissions")
	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected /permissions to be handled without starting an agent run")
	}
	permissionText := transcriptText(next.transcript)
	for _, want := range []string{"Permissions", "status: ok", "Permission mode: ask", "persistent grants: 1", "bash [allow/high]", "[REDACTED]"} {
		assertContains(t, permissionText, want)
	}
	assertNotContains(t, permissionText, "sk-proj-sensitive")
}

func TestCompactCommandAvoidsShellOnlyPlaceholder(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/compact")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /compact to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{"Compact", "status: warning", "requested: yes", "visible transcript rows:"} {
		assertContains(t, text, want)
	}
	if strings.Contains(text, "not wired") || strings.Contains(text, "future compaction backend") {
		t.Fatalf("compact output should describe product state, got %q", text)
	}
}
