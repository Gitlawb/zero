package tui

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/notify"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// execCmd runs a possibly-batched command synchronously and returns the first
// substantive message. Run starts batch the agent command with the spinner
// tick, so tests unwrap the batch and skip spinner housekeeping.
func execCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return msg
	}
	for _, sub := range batch {
		if inner := execCmd(sub); inner != nil {
			if _, isTick := inner.(spinner.TickMsg); !isTick {
				return inner
			}
		}
	}
	return nil
}

type fakeProvider struct {
	events   []zeroruntime.StreamEvent
	requests []zeroruntime.CompletionRequest
}

func (provider *fakeProvider) StreamCompletion(
	ctx context.Context,
	request zeroruntime.CompletionRequest,
) (<-chan zeroruntime.StreamEvent, error) {
	provider.requests = append(provider.requests, request)
	ch := make(chan zeroruntime.StreamEvent, len(provider.events))
	for _, event := range provider.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestPromptSubmitInjectsLiveSessionModelContext(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "I am using the active session model."},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		Cwd:          t.TempDir(),
		ProviderName: "ollama-cloud",
		ModelName:    "glm-5.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
	})
	m.input.SetValue("which model are you")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}

	updated, _ = next.Update(execCmd(cmd))
	_ = updated.(model)

	if len(provider.requests) != 1 {
		t.Fatalf("expected one provider request, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Messages) == 0 {
		t.Fatal("expected provider request to include a system message")
	}
	systemPrompt := provider.requests[0].Messages[0].Content
	for _, want := range []string{
		"Active provider: ollama-cloud",
		"Active model: glm-5.1",
		"Persisted config",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("expected system prompt to contain %q, got:\n%s", want, systemPrompt)
		}
	}
}

func TestPromptSubmitStoresReasoningSeparatelyFromAnswer(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventReasoning, Content: "private "},
		{Type: zeroruntime.StreamEventReasoning, Content: "thought"},
		{Type: zeroruntime.StreamEventText, Content: "public answer"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		Cwd:          t.TempDir(),
		ProviderName: "tokenrouter",
		ModelName:    "MiniMax-M3",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
	})
	base := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	times := []time.Time{
		base,
		base.Add(1 * time.Second),
		base.Add(1800 * time.Millisecond),
		base.Add(2500 * time.Millisecond),
		base.Add(6 * time.Second),
	}
	m.now = func() time.Time {
		if len(times) == 0 {
			return base.Add(6 * time.Second)
		}
		next := times[0]
		times = times[1:]
		return next
	}
	m.input.SetValue("hello")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}

	updated, _ = next.Update(execCmd(cmd))
	next = updated.(model)

	reasoning, ok := findTranscriptRow(next.transcript, rowReasoning)
	if !ok || reasoning.text != "private thought" {
		t.Fatalf("reasoning row = %#v, ok=%v", reasoning, ok)
	}
	if reasoning.turnElapsed != 1500*time.Millisecond {
		t.Fatalf("reasoning elapsed = %s, want 1.5s", reasoning.turnElapsed)
	}
	assistant, ok := findTranscriptRow(next.transcript, rowAssistant)
	if !ok || assistant.text != "public answer" {
		t.Fatalf("assistant row = %#v, ok=%v", assistant, ok)
	}
	if assistant.turnElapsed != 6*time.Second {
		t.Fatalf("assistant elapsed = %s, want 6s", assistant.turnElapsed)
	}
	if strings.Contains(assistant.text, "private thought") {
		t.Fatalf("assistant answer leaked reasoning: %#v", assistant)
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		input string
		kind  commandKind
		text  string
	}{
		{input: "", kind: commandEmpty},
		{input: "   ", kind: commandEmpty},
		{input: "/help", kind: commandHelp},
		{input: "/clear", kind: commandClear},
		{input: "/exit", kind: commandExit},
		{input: "/quit", kind: commandExit},
		{input: "/tools", kind: commandTools},
		{input: "/permissions", kind: commandPermissions},
		{input: "/sandbox-setup", kind: commandSandboxSetup},
		{input: "/context", kind: commandContext},
		{input: "/model", kind: commandModel},
		{input: "/model list", kind: commandModel, text: "list"},
		{input: "/search needle", kind: commandSearch, text: "needle"},
		{input: "/find needle", kind: commandSearch, text: "needle"},
		{input: "/resume", kind: commandResume},
		{input: "/sessions", kind: commandResume},
		{input: "/spec add review flow", kind: commandSpec, text: "add review flow"},
		{input: "/compact", kind: commandCompact},
		{input: "/effort high", kind: commandEffort, text: "high"},
		{input: "/style concise", kind: commandStyle, text: "concise"},
		{input: "/debug-mode", kind: commandDebug},
		{input: "hello zero", kind: commandPrompt, text: "hello zero"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			command := parseCommand(tc.input)
			if command.kind != tc.kind || command.text != tc.text {
				t.Fatalf("expected kind=%v text=%q, got kind=%v text=%q", tc.kind, tc.text, command.kind, command.text)
			}
		})
	}
}

func TestCommandRegistryResolvesAliasesAndFormatsHelp(t *testing.T) {
	names := listCommandNames()
	for _, name := range []string{"/help", "/model", "/provider", "/context", "/debug-mode", "/quit"} {
		if !stringSliceContains(names, name) {
			t.Fatalf("expected command names to contain %s, got %#v", name, names)
		}
	}

	resolved, ok := resolveCommand("/quit")
	if !ok || resolved.kind != commandExit {
		t.Fatalf("expected /quit to resolve to exit, got ok=%v command=%#v", ok, resolved)
	}

	help := strings.Join(formatCommandHelpLines(), "\n")
	for _, want := range []string{"/model", "/context", "/debug", "/permissions", "/spec", "model"} {
		assertContains(t, help, want)
	}
}

func TestTranscriptReducer(t *testing.T) {
	transcript := initialTranscript()
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendUser, text: "hello"})
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendAssistant, text: "hi"})
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendSystem, text: "note"})
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendError, text: "boom"})

	if len(transcript) != 5 {
		t.Fatalf("expected welcome plus four rows, got %#v", transcript)
	}
	if transcript[1].kind != rowUser || transcript[1].text != "hello" {
		t.Fatalf("expected user row, got %#v", transcript[1])
	}
	if transcript[3].kind != rowSystem || transcript[3].text != "note" {
		t.Fatalf("expected system row, got %#v", transcript[3])
	}

	cleared := reduceTranscript(transcript, transcriptAction{kind: actionClear})
	if len(cleared) != 1 || cleared[0].kind != rowWelcome {
		t.Fatalf("expected clear to reset to welcome row, got %#v", cleared)
	}
}

func TestInitialRenderShowsLimeChatSurface(t *testing.T) {
	model := newModel(context.Background(), Options{
		Cwd:          `/workspace/zero`,
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
	})
	model.width = 120
	model.height = 34

	view := viewString(model.View())
	assertContains(t, view, `/workspace/zero`)
	assertContains(t, view, "openai/gpt-4.1")
	assertContains(t, view, emptyStateTagline)
	assertNotContains(t, view, "running zero against ")
	assertNotContains(t, view, " 0 ")
	assertContains(t, view, composerPlaceholder)
	assertNotContains(t, view, "interactive")
	if strings.Contains(view, "Welcome to Zero") {
		t.Fatalf("empty chat surface should not show welcome transcript clutter, got %q", view)
	}
}

func TestEmptyStateCollapsesAfterFirstPrompt(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 100
	m.height = 30
	m.input.SetValue("inspect the repo")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	next.width = m.width
	next.height = m.height

	if next.pending {
		t.Fatal("expected missing provider prompt not to start an agent run")
	}
	if !transcriptContains(next.transcript, "inspect the repo") || !transcriptContains(next.transcript, "No provider configured.") {
		t.Fatalf("expected prompt and notice rows in transcript, got %#v", next.transcript)
	}
	if next.flushed != len(next.transcript) {
		t.Fatalf("expected settled rows to flush to scrollback, flushed=%d rows=%d", next.flushed, len(next.transcript))
	}
	view := viewString(next.View())
	if strings.Contains(view, emptyStateTagline) {
		t.Fatalf("empty state should collapse after first prompt, got %q", view)
	}
	// Working view shows provider status and the composer divider model fallback.
	assertNotContains(t, view, "interactive")
	assertContains(t, view, "no model")
}

func TestEmptyStateStaysVisibleOnEmptySubmit(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 96
	m.height = 30
	m.input.SetValue("   ")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	next.width = m.width
	next.height = m.height

	view := viewString(next.View())
	assertContains(t, view, emptyStateTagline)
	assertNotContains(t, view, "❯ inspect")
}

func TestHelpCommandAppendsHelpRow(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/help")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !transcriptContains(next.transcript, "/tools") {
		t.Fatalf("expected help transcript to mention /tools, got %#v", next.transcript)
	}
	if !transcriptContains(next.transcript, "/model") || !transcriptContains(next.transcript, "/context") {
		t.Fatalf("expected help transcript to mention model and context commands, got %#v", next.transcript)
	}
}

func TestClearCommandResetsTranscript(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "hello"})
	m.input.SetValue("/clear")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if len(next.transcript) != 1 || next.transcript[0].kind != rowWelcome {
		t.Fatalf("expected clear to reset transcript, got %#v", next.transcript)
	}
}

func TestToolsCommandListsRegisteredTools(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))
	m := newModel(context.Background(), Options{Registry: registry})
	m.input.SetValue("/tools")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !transcriptContains(next.transcript, "read_file") {
		t.Fatalf("expected tools transcript to list read_file, got %#v", next.transcript)
	}
}

func TestPermissionsCommandListsPersistentSandboxGrants(t *testing.T) {
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	if _, err := store.Grant(sandbox.GrantInput{
		ToolName:    "bash",
		Decision:    sandbox.GrantAllow,
		MaxAutonomy: sandbox.AutonomyHigh,
		Reason:      "sk-proj-sensitive trusted shell",
	}); err != nil {
		t.Fatalf("Grant bash returned error: %v", err)
	}
	if _, err := store.Grant(sandbox.GrantInput{
		ToolName:    "write_file",
		Decision:    sandbox.GrantDeny,
		MaxAutonomy: sandbox.AutonomyLow,
	}); err != nil {
		t.Fatalf("Grant write_file returned error: %v", err)
	}
	m := newModel(context.Background(), Options{
		PermissionMode: agent.PermissionModeAsk,
		SandboxStore:   store,
	})
	m.input.SetValue("/permissions")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /permissions to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{
		"Permissions",
		"ask permissions",
		"mode  ask",
		"Grants",
		"bash [allow/high]",
		"write_file [deny/low]",
		"[REDACTED]",
	} {
		assertContains(t, text, want)
	}
	assertNotContains(t, text, "sk-proj-sensitive")
	assertNotContains(t, text, "status: ok")
	assertNotContains(t, text, "Permission mode:")
}

func TestPlanCommandShowsCurrentPlan(t *testing.T) {
	registry := tools.NewRegistry()
	planTool := tools.NewUpdatePlanTool()
	result := planTool.Run(context.Background(), map[string]any{
		"plan": []any{
			map[string]any{
				"id":      "one",
				"content": "Wire model catalog",
				"status":  "completed",
			},
			map[string]any{
				"id":      "two",
				"content": "Add max turns",
				"status":  "in_progress",
				"notes":   "Go exec parity",
			},
		},
	})
	if result.Status != tools.StatusOK {
		t.Fatalf("update_plan setup failed: %#v", result)
	}
	registry.Register(planTool)
	m := newModel(context.Background(), Options{Registry: registry})
	m.input.SetValue("/plan")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /plan to be handled without starting an agent run")
	}
	for _, want := range []string{"Current Plan", "Wire model catalog", "Add max turns", "in_progress", "Go exec parity"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected plan transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestPlanCommandHandlesMissingPlanTool(t *testing.T) {
	m := newModel(context.Background(), Options{Registry: tools.NewRegistry()})
	m.input.SetValue("/plan")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !transcriptContains(next.transcript, "No plan is active") {
		t.Fatalf("expected missing plan message, got %#v", next.transcript)
	}
}

func TestContextCommandShowsSessionState(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))
	m := newModel(context.Background(), Options{
		Cwd:            `D:\codings\Opensource\Zero`,
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		Registry:       registry,
		PermissionMode: agent.PermissionModeAsk,
	})
	m.input.SetValue("/context")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /context to be handled without starting an agent run")
	}
	for _, want := range []string{
		`D:\codings\Opensource\Zero`,
		"go runtime | ask permissions | 1 tool",
		"provider   openai",
		"model      gpt-4.1",
		"max turns  ",
		"root        ",
		"registered  1",
	} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected context transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestModelCommandShowsActiveModelWithoutRunningAgent(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
	})
	m.input.SetValue("/model list")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	for _, want := range []string{"Active model: gpt-4.1", "provider: openai", "Available models", "* gpt-4.1"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected model transcript to contain %q, got %#v", want, next.transcript)
		}
	}
	if !transcriptHasMarkedModelEntry(next.transcript) {
		t.Fatalf("expected model transcript to contain a marked model entry, got %#v", next.transcript)
	}
	if transcriptContains(next.transcript, "Model switching") {
		t.Fatalf("expected /model list to show catalog, got switching placeholder: %#v", next.transcript)
	}
}

func TestModelCommandSwitchesSessionModel(t *testing.T) {
	var rebuilt config.ProviderProfile
	nextProvider := &fakeProvider{}
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			APIKey:       "sk-test",
			Model:        "gpt-4.1",
		},
		Provider: &fakeProvider{},
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			rebuilt = profile
			return nextProvider, nil
		},
	})
	m.input.SetValue("/model gpt-4.1-mini")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	if next.modelName != "gpt-4.1-mini" || next.provider != nextProvider {
		t.Fatalf("expected model/provider to update, got model=%q provider=%#v", next.modelName, next.provider)
	}
	if rebuilt.Model != "gpt-4.1-mini" {
		t.Fatalf("expected provider rebuild with selected model, got %#v", rebuilt)
	}
	for _, want := range []string{"Switched model", "model: gpt-4.1-mini", "api model: gpt-4.1-mini"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected model transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestModelCommandPersistsSelectedModelToUserConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero.json")
	if _, err := config.UpsertProvider(configPath, config.ProviderProfile{
		Name:         "openai",
		ProviderKind: config.ProviderKindOpenAI,
		BaseURL:      config.OpenAIBaseURL,
		APIKey:       "sk-test",
		Model:        "gpt-4.1",
	}, true); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	m := newModel(context.Background(), Options{
		UserConfigPath: configPath,
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			APIKey:       "sk-test",
			Model:        "gpt-4.1",
		},
		Provider: &fakeProvider{},
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return &fakeProvider{}, nil
		},
	})
	m.input.SetValue("/model gpt-4.1-mini")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	if next.modelName != "gpt-4.1-mini" {
		t.Fatalf("modelName = %q, want gpt-4.1-mini", next.modelName)
	}
	persisted, err := config.Resolve(config.ResolveOptions{UserConfigPath: configPath})
	if err != nil {
		t.Fatalf("resolve persisted config: %v", err)
	}
	if got := persisted.Provider.Model; got != "gpt-4.1-mini" {
		t.Fatalf("persisted provider model = %q, want gpt-4.1-mini", got)
	}
	if !transcriptContains(next.transcript, "saved: user config") {
		t.Fatalf("expected model transcript to mention saved user config, got %#v", next.transcript)
	}
}

type stubModelSwitchCompactionGuard struct {
	decision modelSwitchCompactionDecision
	requests []modelSwitchCompactionRequest
}

func (guard *stubModelSwitchCompactionGuard) BeforeModelSwitch(request modelSwitchCompactionRequest) modelSwitchCompactionDecision {
	guard.requests = append(guard.requests, request)
	return guard.decision
}

func TestDefaultModelSwitchCompactionPolicyRequestsCompactionForLargeTargetWindowUsage(t *testing.T) {
	decision := defaultModelSwitchCompactionPolicy{}.BeforeModelSwitch(modelSwitchCompactionRequest{
		CurrentModel:        "gpt-4.1",
		TargetModel:         "small-context-model",
		TargetContextWindow: 1000,
		EstimatedTokens:     850,
		SessionEventCount:   20,
		CompactRequests:     0,
	})

	if !decision.RequestCompaction {
		t.Fatalf("expected default policy to request compaction, got %#v", decision)
	}
	if !strings.Contains(decision.Reason, "target context") {
		t.Fatalf("expected reason to mention target context, got %q", decision.Reason)
	}
}

func TestModelCommandRequestsCompactionBeforeDirtyContextSwitch(t *testing.T) {
	guard := &stubModelSwitchCompactionGuard{
		decision: modelSwitchCompactionDecision{
			RequestCompaction: true,
			Reason:            "dirty context uses most of the target window",
		},
	}
	previousGuard := modelSwitchCompactionGuard
	modelSwitchCompactionGuard = guard
	defer func() { modelSwitchCompactionGuard = previousGuard }()

	originalProvider := &fakeProvider{}
	rebuilds := 0
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			APIKey:       "sk-test",
			Model:        "gpt-4.1",
		},
		Provider: originalProvider,
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			rebuilds++
			return &fakeProvider{}, nil
		},
	})
	m.sessionEvents = []sessions.Event{{Sequence: 1, Type: sessions.EventMessage}}
	m.input.SetValue("/model gpt-4.1-mini")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	if rebuilds != 0 {
		t.Fatalf("provider should not be rebuilt before requested compaction, got %d rebuilds", rebuilds)
	}
	if next.modelName != "gpt-4.1" || next.provider != originalProvider {
		t.Fatalf("expected active model/provider to remain unchanged, got model=%q provider=%#v", next.modelName, next.provider)
	}
	if next.compactRequests != 1 {
		t.Fatalf("expected model switch to request compaction, got %d requests", next.compactRequests)
	}
	if len(guard.requests) != 1 {
		t.Fatalf("expected one compaction guard request, got %d", len(guard.requests))
	}
	request := guard.requests[0]
	if request.CurrentModel != "gpt-4.1" || request.TargetModel != "gpt-4.1-mini" {
		t.Fatalf("unexpected guard model transition: %#v", request)
	}
	if request.SessionEventCount != 1 {
		t.Fatalf("expected dirty session event count in guard request, got %#v", request)
	}
	for _, want := range []string{
		"Context compaction requested before switching models.",
		"dirty context uses most of the target window",
	} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected model transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestModelCommandRequiresProviderRebuildForSwitch(t *testing.T) {
	m := newModel(context.Background(), Options{
		ModelName: "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			Model:        "gpt-4.1",
		},
	})
	m.input.SetValue("/model gpt-4.1-mini")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	if next.modelName != "gpt-4.1" {
		t.Fatalf("expected active model to remain unchanged, got %q", next.modelName)
	}
	if !transcriptContains(next.transcript, "Provider rebuild is not available") {
		t.Fatalf("expected provider rebuild availability error, got %#v", next.transcript)
	}
}

func TestModelCommandRejectsSwitchWhilePending(t *testing.T) {
	m := newModel(context.Background(), Options{
		ModelName: "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			Model:        "gpt-4.1",
		},
	})
	m.pending = true
	m.input.SetValue("/model gpt-4.1-mini")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	if next.modelName != "gpt-4.1" {
		t.Fatalf("expected active model to remain unchanged, got %q", next.modelName)
	}
	if !transcriptContains(next.transcript, "Cannot switch models while a run is active") {
		t.Fatalf("expected pending switch error, got %#v", next.transcript)
	}
}

func TestModelCommandReportsProviderRebuildErrors(t *testing.T) {
	m := newModel(context.Background(), Options{
		ModelName: "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			Model:        "gpt-4.1",
		},
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return nil, errors.New("rebuild failed")
		},
	})
	m.input.SetValue("/model gpt-4.1-mini")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.modelName != "gpt-4.1" {
		t.Fatalf("expected active model to remain unchanged, got %q", next.modelName)
	}
	if !transcriptContains(next.transcript, "rebuild failed") {
		t.Fatalf("expected rebuild error, got %#v", next.transcript)
	}
}

func TestDoctorCommandUsesCurrentProviderProfile(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			Model:        "gpt-4.1",
		},
	})
	m.input.SetValue("/doctor")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /doctor to be handled without starting an agent run")
	}
	for _, want := range []string{"Diagnostics", "Provider", "provider.connectivity", "Actions"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected doctor transcript to contain %q, got %#v", want, next.transcript)
		}
	}
	for _, unwanted := range []string{"provider.config", "provider.model", "Generated", "Checks"} {
		if transcriptContains(next.transcript, unwanted) {
			t.Fatalf("expected doctor transcript to hide %q, got %#v", unwanted, next.transcript)
		}
	}
}

func TestSearchCommandUsesSessionStore(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "Searchable", Cwd: "repo", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{
		Type: sessions.EventMessage,
		Payload: map[string]any{
			"role":    "assistant",
			"content": "needle appears here",
		},
	}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}
	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("/search needle")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /search to be handled without starting an agent run")
	}
	if !transcriptContains(next.transcript, "Found 1 local session event") || !transcriptContains(next.transcript, "needle appears here") {
		t.Fatalf("expected search hit in transcript, got %#v", next.transcript)
	}
}

func TestSearchCommandRequiresQuery(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/search")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !transcriptContains(next.transcript, "usage: /search <query>") {
		t.Fatalf("expected search usage, got %#v", next.transcript)
	}
}

func TestResumeCommandListsRecentSessions(t *testing.T) {
	store := testSessionStore(t)
	first, err := store.Create(sessions.CreateInput{Title: "Older", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create older returned error: %v", err)
	}
	if _, err := store.AppendEvent(first.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"content": "old"}}); err != nil {
		t.Fatalf("Append older returned error: %v", err)
	}
	second, err := store.Create(sessions.CreateInput{Title: "Newer", ModelID: "claude-sonnet-4.5", Provider: "anthropic"})
	if err != nil {
		t.Fatalf("Create newer returned error: %v", err)
	}
	if _, err := store.AppendEvent(second.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"content": "new"}}); err != nil {
		t.Fatalf("Append newer returned error: %v", err)
	}
	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("/resume")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /resume to be handled without starting an agent run")
	}
	// Bare /resume now opens the interactive session picker (like /model & /provider).
	if next.picker == nil || next.picker.kind != pickerSession {
		t.Fatalf("expected /resume to open the session picker, got picker=%#v", next.picker)
	}
	// Every row carries the session title (in the Label, after the timestamp) and
	// resolves to / shows the session id (Value + Meta), for both sessions.
	findByID := func(id string) (pickerItem, bool) {
		for _, item := range next.picker.items {
			if item.Value == id {
				return item, true
			}
		}
		return pickerItem{}, false
	}
	for _, want := range []struct{ title, id string }{{"Newer", second.SessionID}, {"Older", first.SessionID}} {
		item, ok := findByID(want.id)
		if !ok {
			t.Fatalf("picker missing session id %q; items=%#v", want.id, next.picker.items)
		}
		if !strings.Contains(item.Label, want.title) {
			t.Fatalf("picker Label %q should contain the title %q", item.Label, want.title)
		}
		if !strings.Contains(item.Meta, want.id) {
			t.Fatalf("picker %q Meta should show the id %q, got %q", want.title, want.id, item.Meta)
		}
	}
	// The picker overlay renders the titles and ids.
	view := viewString(next.View())
	for _, want := range []string{"Resume a session", "Newer", "Older", first.SessionID, second.SessionID} {
		if !strings.Contains(view, want) {
			t.Fatalf("session picker view missing %q:\n%s", want, view)
		}
	}
}

func TestResumePickerSelectionHydratesSession(t *testing.T) {
	store := testSessionStore(t)
	target, err := store.Create(sessions.CreateInput{Title: "Pick me", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	if _, err := store.AppendEvent(target.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"content": "hello"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.Create(sessions.CreateInput{Title: "Other", ModelID: "x", Provider: "y"}); err != nil {
		t.Fatalf("Create other: %v", err)
	}

	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("/resume")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil || m.picker.kind != pickerSession {
		t.Fatalf("expected the session picker to open, got %#v", m.picker)
	}
	for i, item := range m.picker.items {
		if item.Value == target.SessionID {
			m.picker.selected = i
		}
	}

	updated, cmd := m.Update(testKey(tea.KeyEnter)) // choosePicker
	next := updated.(model)
	if cmd != nil {
		t.Fatal("selecting a session to resume should not start an agent run")
	}
	if next.picker != nil {
		t.Fatal("picker should close after a selection")
	}
	if next.activeSession.SessionID != target.SessionID {
		t.Fatalf("active session = %q, want %q", next.activeSession.SessionID, target.SessionID)
	}
	if !transcriptContains(next.transcript, "Resumed Zero session") || !transcriptContains(next.transcript, target.SessionID) {
		t.Fatalf("expected the resume summary in the transcript, got %#v", next.transcript)
	}
}

func TestResumePickerHidesEmptyFailedSessions(t *testing.T) {
	store := testSessionStore(t)
	real, err := store.Create(sessions.CreateInput{Title: "Real one", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create real: %v", err)
	}
	if _, err := store.AppendEvent(real.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "do a thing"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.AppendEvent(real.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"role": "assistant", "content": "here is the thing"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// An empty/failed run: prompt + the no-output guardrail stop, nothing else.
	empty, err := store.Create(sessions.CreateInput{Title: "Empty one", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create empty: %v", err)
	}
	if _, err := store.AppendEvent(empty.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "do a thing"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.AppendEvent(empty.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"role": "assistant", "content": "Agent stopped after 3 turns with no output (no visible text and no tool calls) to avoid consuming tokens without making progress."}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	picker := newModel(context.Background(), Options{SessionStore: store}).newSessionPicker()
	if picker == nil {
		t.Fatal("expected a picker containing the real session")
	}
	for _, item := range picker.items {
		if item.Value == empty.SessionID {
			t.Fatalf("empty/no-output session must be hidden from the picker: %#v", picker.items)
		}
	}
	shown := false
	for _, item := range picker.items {
		if item.Value == real.SessionID {
			shown = true
		}
	}
	if !shown {
		t.Fatalf("the real session must be shown: %#v", picker.items)
	}
}

func TestResumeHonorsPriorCompaction(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "Compacted", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, content := range []string{"alpha", "beta", "gamma", "delta"} {
		if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	plan, err := store.PlanCompaction(session.SessionID, sessions.CompactionOptions{PreserveLast: 2, MaxPromptChars: 500})
	if err != nil {
		t.Fatalf("PlanCompaction: %v", err)
	}
	if _, err := store.RecordCompaction(session.SessionID, sessions.RecordCompactionInput{Plan: plan, Summary: "early summary"}); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]string{"content": "epsilon"}}); err != nil {
		t.Fatalf("Append epsilon: %v", err)
	}

	raw, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	rehydrated, err := store.ReadRehydratedEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadRehydratedEvents: %v", err)
	}
	if len(rehydrated) >= len(raw) {
		t.Fatalf("test setup invalid: rehydrated (%d) should be < raw (%d)", len(rehydrated), len(raw))
	}

	m := newModel(context.Background(), Options{SessionStore: store})
	next, _ := m.handleResumeCommand(session.SessionID)
	// Resume must load the rehydrated (compaction-aware) context, not the raw log —
	// matching the CLI's --resume and the in-TUI /compact reload. Compare contents,
	// not just length: a regression that returned a same-length but reordered or
	// substituted slice (e.g. a dropped original in place of the summary) would
	// slip past a length check.
	if !reflect.DeepEqual(next.sessionEvents, rehydrated) {
		t.Fatalf("resumed sessionEvents do not match the rehydrated context (resume must honor prior compaction)\nresumed:    %+v\nrehydrated: %+v\nraw:        %+v", next.sessionEvents, rehydrated, raw)
	}
}

func TestResumeCommandWithUnknownIDReportsMissingSession(t *testing.T) {
	m := newModel(context.Background(), Options{SessionStore: testSessionStore(t)})
	m.input.SetValue("/resume zero_123")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !transcriptContains(next.transcript, "zero session not found: zero_123") {
		t.Fatalf("expected missing session message, got %#v", next.transcript)
	}
}

func TestPromptSubmitAppendsUserAndAssistantRows(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "hello"},
		{Type: zeroruntime.StreamEventText, Content: " back"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: testSessionStore(t),
	})
	m.input.SetValue("say hi")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if !transcriptContains(next.transcript, "say hi") {
		t.Fatalf("expected user row after submit, got %#v", next.transcript)
	}
	if cmd == nil {
		t.Fatal("expected submit to return agent command")
	}

	msg := execCmd(cmd)
	updated, _ = next.Update(msg)
	next = updated.(model)
	if !transcriptContains(next.transcript, "hello back") {
		t.Fatalf("expected assistant row after agent response, got %#v", next.transcript)
	}
}

func TestPromptSubmitDoesNotStartAnotherRunWhilePending(t *testing.T) {
	m := newModel(context.Background(), Options{
		Provider: &fakeProvider{},
		Registry: tools.NewRegistry(),
	})
	m.pending = true
	m.input.SetValue("second prompt")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected no command while another run is pending")
	}
	if transcriptContains(next.transcript, "second prompt") {
		t.Fatalf("pending prompt should not be appended, got %#v", next.transcript)
	}
	if !next.pending {
		t.Fatal("expected existing pending run to remain pending")
	}
}

func TestEscCancelsPendingRun(t *testing.T) {
	m := newModel(context.Background(), Options{})
	cancelled := false
	m.pending = true
	m.activeRunID = 1
	m.runCancel = func() { cancelled = true }

	updated, _ := m.Update(testKey(tea.KeyEsc))
	next := updated.(model)

	if !cancelled {
		t.Fatal("expected Esc to cancel pending run")
	}
	if next.pending {
		t.Fatal("expected Esc to clear pending state")
	}
	if next.activeRunID != 0 || next.runCancel != nil {
		t.Fatalf("expected active run state to clear, got id=%d cancel=%v", next.activeRunID, next.runCancel)
	}
}

func TestStaleAgentResponseAfterCancelIsIgnored(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pending = false
	m.activeRunID = 0
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "new prompt"})

	updated, _ := m.Update(agentResponseMsg{
		runID: 1,
		rows:  []transcriptRow{{kind: rowAssistant, text: "stale response"}},
	})
	next := updated.(model)

	if transcriptContains(next.transcript, "stale response") {
		t.Fatalf("stale response should be ignored, got %#v", next.transcript)
	}
}

func TestAgentResponsePreservesToolResultMetadata(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/file.txt",
		"+++ b/file.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
	}, "\n")
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7

	updated, _ := m.Update(agentResponseMsg{
		runID: 7,
		rows: []transcriptRow{{
			kind:   rowToolResult,
			text:   "tool result: apply_patch error",
			tool:   "apply_patch",
			status: tools.StatusError,
			detail: diff,
		}},
	})
	next := updated.(model)

	row, ok := findTranscriptRow(next.transcript, rowToolResult)
	if !ok {
		t.Fatalf("expected tool result row, got %#v", next.transcript)
	}
	if row.tool != "apply_patch" || row.status != tools.StatusError || row.detail != diff {
		t.Fatalf("tool result metadata was not preserved: %#v", row)
	}
	assertContains(t, next.renderRow(row, 80, buildRowContext(next.transcript)), "@@ -1 +1 @@")
}

func TestAgentResponsePreservesPermissionMetadata(t *testing.T) {
	event := agent.PermissionEvent{
		ToolCallID:     "call_1",
		ToolName:       "write_file",
		Action:         agent.PermissionActionPrompt,
		Permission:     "prompt",
		PermissionMode: agent.PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		SideEffect:     "write",
		Reason:         "Creates or overwrites files.",
		Risk:           sandbox.Risk{Level: sandbox.RiskHigh},
	}
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7

	updated, _ := m.Update(agentResponseMsg{
		runID: 7,
		rows:  []transcriptRow{permissionTranscriptRow(event)},
	})
	next := updated.(model)

	row, ok := findTranscriptRow(next.transcript, rowPermission)
	if !ok {
		t.Fatalf("expected permission row, got %#v", next.transcript)
	}
	if row.tool != "write_file" || row.permission == nil || row.permission.ToolCallID != "call_1" {
		t.Fatalf("permission metadata was not preserved: %#v", row)
	}
	rendered := next.renderRow(row, 96, buildRowContext(next.transcript))
	for _, want := range []string{"permission", "write_file", "prompt", "risk:high", "mode=ask", "Creates or overwrites"} {
		assertContains(t, rendered, want)
	}
}

func TestPermissionRequestShowsFocusedPrompt(t *testing.T) {
	request := testPromptPermissionRequest()
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96

	updated, cmd := m.Update(permissionRequestMsg{
		runID:   7,
		request: request,
	})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected permission request to update TUI state synchronously")
	}
	if next.pendingPermission == nil {
		t.Fatalf("expected permission prompt to be pending, got %#v", next)
	}
	if countTranscriptRows(next.transcript, rowPermission) != 1 {
		t.Fatalf("expected permission request to append one permission row, got %#v", next.transcript)
	}
	view := next.View()
	for _, want := range []string{"write_file", "allow once", "[a]", "deny", "[d]", "always", "[y]", "risk:high", "Creates or overwrites files."} {
		assertContains(t, view, want)
	}
}

func TestPermissionPromptChoicesResolveDecision(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want permissionDecision
	}{
		{name: "allow", key: "a", want: permissionDecisionAllow},
		{name: "deny", key: "d", want: permissionDecisionDeny},
		{name: "always", key: "y", want: permissionDecisionAlwaysAllow},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decisions := []permissionDecision{}
			m := newModel(context.Background(), Options{})
			m.pending = true
			m.activeRunID = 7
			updated, _ := m.Update(permissionRequestMsg{
				runID:   7,
				request: testPromptPermissionRequest(),
				decide: func(decision agent.PermissionDecision) {
					decisions = append(decisions, permissionDecision(decision.Action))
				},
			})
			next := updated.(model)

			updated, cmd := next.Update(testKeyText(tc.key))
			next = updated.(model)

			if cmd != nil {
				t.Fatal("expected permission choice to resolve synchronously")
			}
			if len(decisions) != 1 || decisions[0] != tc.want {
				t.Fatalf("expected decision %q, got %#v", tc.want, decisions)
			}
			if next.pendingPermission != nil {
				t.Fatalf("expected permission prompt to clear after choice, got %#v", next.pendingPermission)
			}
		})
	}
}

func TestPermissionPromptBlocksNormalSubmit(t *testing.T) {
	decisions := []permissionDecision{}
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	updated, _ := m.Update(permissionRequestMsg{
		runID:   7,
		request: testPromptPermissionRequest(),
		decide: func(decision agent.PermissionDecision) {
			decisions = append(decisions, permissionDecision(decision.Action))
		},
	})
	next := updated.(model)
	next.input.SetValue("second prompt")

	updated, cmd := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)

	if cmd != nil {
		t.Fatal("expected permission confirm to resolve synchronously (no cmd)")
	}
	// Enter confirms the highlighted option (default: allow once) — it must NOT
	// submit the composer's pending text as a new prompt.
	if len(decisions) != 1 || decisions[0] != permissionDecisionAllow {
		t.Fatalf("expected Enter to confirm the default option (allow once), got %#v", decisions)
	}
	if transcriptContains(next.transcript, "second prompt") {
		t.Fatalf("permission prompt should block normal prompt submit, got %#v", next.transcript)
	}
	if next.pendingPermission != nil {
		t.Fatalf("expected permission prompt to clear after confirm, got %#v", next.pendingPermission)
	}
}

func TestPermissionRowRendersSandboxViolations(t *testing.T) {
	violation := sandbox.Violation{
		Code:        sandbox.ViolationOutsideWorkspace,
		ToolName:    "write_file",
		Action:      sandbox.ActionDeny,
		Risk:        sandbox.Risk{Level: sandbox.RiskCritical},
		Path:        "../secret.txt",
		Reason:      "writes must stay inside workspace",
		Recoverable: false,
	}
	event := agent.PermissionEvent{
		ToolCallID:     "call_2",
		ToolName:       "write_file",
		Action:         agent.PermissionActionDeny,
		Permission:     "prompt",
		PermissionMode: agent.PermissionModeUnsafe,
		Autonomy:       string(sandbox.AutonomyHigh),
		SideEffect:     "write",
		Reason:         "workspace boundary enforced",
		Risk:           sandbox.Risk{Level: sandbox.RiskHigh},
		Violation:      &violation,
	}

	rendered := newModel(context.Background(), Options{}).renderRow(permissionTranscriptRow(event), 96, buildRowContext(nil))

	for _, want := range []string{"write_file", "denied", "risk:high", "violation=outside_workspace risk=critical", "../secret.txt"} {
		assertContains(t, rendered, want)
	}
}

func TestAppendTranscriptRowDedupesRuntimeRowsByID(t *testing.T) {
	event := agent.PermissionEvent{
		ToolCallID: "call_1",
		ToolName:   "write_file",
		Action:     agent.PermissionActionPrompt,
	}
	rows := initialTranscript()
	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolCall, id: "call_1", text: "tool call: write_file", tool: "write_file"})
	rows = appendTranscriptRow(rows, permissionTranscriptRow(event))
	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolResult, id: "call_1", text: "tool result: write_file error", tool: "write_file", status: tools.StatusError})

	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolCall, id: "call_1", text: "tool call: write_file", tool: "write_file"})
	rows = appendTranscriptRow(rows, permissionTranscriptRow(event))
	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolResult, id: "call_1", text: "tool result: write_file error", tool: "write_file", status: tools.StatusError})

	if len(rows) != 4 {
		t.Fatalf("expected welcome plus three unique runtime rows, got %#v", rows)
	}
}

func TestAgentEventRenderingMappingCoversRuntimeContract(t *testing.T) {
	surfaces := map[zeroruntime.AgentEventType]string{
		zeroruntime.AgentEventText:       "assistant transcript row",
		zeroruntime.AgentEventToolCall:   "tool call transcript row",
		zeroruntime.AgentEventToolResult: "tool result transcript row",
		zeroruntime.AgentEventThinking:   "deferred: no transcript row until runtime emits thinking deltas",
		zeroruntime.AgentEventUsage:      "usage tracker footer segment",
		zeroruntime.AgentEventPlanUpdate: "system transcript row from /plan",
		zeroruntime.AgentEventError:      "error transcript row",
		zeroruntime.AgentEventTurnEnd:    "control boundary, no transcript row",
	}
	for _, eventType := range []zeroruntime.AgentEventType{
		zeroruntime.AgentEventText,
		zeroruntime.AgentEventToolCall,
		zeroruntime.AgentEventToolResult,
		zeroruntime.AgentEventThinking,
		zeroruntime.AgentEventUsage,
		zeroruntime.AgentEventPlanUpdate,
		zeroruntime.AgentEventError,
		zeroruntime.AgentEventTurnEnd,
	} {
		if strings.TrimSpace(surfaces[eventType]) == "" {
			t.Fatalf("missing TUI rendering surface note for %s", eventType)
		}
	}

	renderedRows := map[zeroruntime.AgentEventType]struct {
		row   transcriptRow
		wants []string
	}{
		zeroruntime.AgentEventText: {
			row:   transcriptRow{kind: rowAssistant, text: "assistant text"},
			wants: []string{"assistant text"},
		},
		zeroruntime.AgentEventToolCall: {
			row: transcriptRow{
				kind:   rowToolCall,
				text:   "tool call: read_file",
				tool:   "read_file",
				detail: "README.md",
			},
			wants: []string{"read_file", "README.md"},
		},
		zeroruntime.AgentEventToolResult: {
			row: transcriptRow{
				kind:   rowToolResult,
				text:   "tool result: apply_patch error",
				tool:   "apply_patch",
				status: tools.StatusError,
				detail: strings.Join([]string{
					"--- a/file.txt",
					"+++ b/file.txt",
					"@@ -1 +1 @@",
					"-old",
					"+new",
				}, "\n"),
			},
			wants: []string{"apply_patch", "@@ -1 +1 @@"},
		},
		zeroruntime.AgentEventPlanUpdate: {
			row:   transcriptRow{kind: rowSystem, text: "Plan updated\n- inspect: completed"},
			wants: []string{"Plan updated", "inspect"},
		},
		zeroruntime.AgentEventError: {
			row:   transcriptRow{kind: rowError, text: "provider failed"},
			wants: []string{"provider failed"},
		},
	}
	for eventType, tc := range renderedRows {
		t.Run(string(eventType), func(t *testing.T) {
			rendered := newModel(context.Background(), Options{}).renderRow(tc.row, 96, buildRowContext(nil))
			for _, want := range tc.wants {
				assertContains(t, rendered, want)
			}
		})
	}

	m := newModel(context.Background(), Options{
		ModelName:      "gpt-4.1",
		PermissionMode: agent.PermissionModeAsk,
	})
	m.width = 96
	m, usageRows := m.recordUsageEvent("gpt-4.1", zeroruntime.Usage{InputTokens: 100, OutputTokens: 20})
	if len(usageRows) != 0 {
		t.Fatalf("valid usage should update footer without transcript rows, got %#v", usageRows)
	}
	assertContains(t, m.usageStatusSegment(), "120 tok")
	assertContains(t, m.composerDividerLine(96), "gpt-4.1")
	assertContains(t, m.composerDividerLine(96), "ask")
}

func TestToolResultRowDefaultsEmptyStatusToOK(t *testing.T) {
	text := toolResultRowText(agent.ToolResult{Name: "read_file", Output: "done"})

	if !strings.Contains(text, "read_file ok done") {
		t.Fatalf("expected empty status to render as ok, got %q", text)
	}
}

func TestToolResultRowTruncatesLongOutput(t *testing.T) {
	text := toolResultRowText(agent.ToolResult{Name: "read_file", Output: strings.Repeat("x", tuiToolOutputLimit+20)})

	if !strings.Contains(text, "[truncated]") || len(text) >= tuiToolOutputLimit+80 {
		t.Fatalf("expected truncated tool output, got len=%d text=%q", len(text), text)
	}
}

func TestShiftTabCyclesPermissionMode(t *testing.T) {
	m := newModel(context.Background(), Options{PermissionMode: agent.PermissionModeAuto})
	m.width = 96

	// shift+tab toggles Auto<->Ask only; Unsafe is intentionally NOT reachable by
	// a casual keypress (it disables permission prompts).
	for _, want := range []agent.PermissionMode{
		agent.PermissionModeAsk,
		agent.PermissionModeAuto,
	} {
		updated, cmd := m.Update(testKeyShift(tea.KeyTab))
		m = updated.(model)
		if cmd != nil {
			t.Fatalf("expected shift+tab to cycle mode synchronously, got command")
		}
		if m.permissionMode != want {
			t.Fatalf("expected permission mode %q after shift+tab, got %q", want, m.permissionMode)
		}
		if m.permissionMode == agent.PermissionModeUnsafe {
			t.Fatalf("shift+tab must never land on Unsafe")
		}
	}

	// The rendered status label tracks the cycled mode.
	label, _ := m.modeLabel()
	if label != "auto-approve" {
		t.Fatalf("expected mode label to track cycled mode, got %q", label)
	}
}

func TestShiftTabDoesNotCycleWhileModalsActive(t *testing.T) {
	// Permission modal, ask_user prompt, and an open picker all take precedence:
	// shift+tab must not change the mode while any is up.
	t.Run("permission", func(t *testing.T) {
		m := newModel(context.Background(), Options{PermissionMode: agent.PermissionModeAuto})
		m.pending = true
		m.activeRunID = 7
		updated, _ := m.Update(permissionRequestMsg{runID: 7, request: testPromptPermissionRequest()})
		next := updated.(model)
		updated, _ = next.Update(testKeyShift(tea.KeyTab))
		next = updated.(model)
		if next.permissionMode != agent.PermissionModeAuto {
			t.Fatalf("expected mode unchanged while permission modal is up, got %q", next.permissionMode)
		}
		if next.pendingPermission == nil {
			t.Fatal("expected permission prompt to remain pending")
		}
	})
	t.Run("ask_user", func(t *testing.T) {
		m := newModel(context.Background(), Options{PermissionMode: agent.PermissionModeAuto})
		m.pending = true
		m.activeRunID = 7
		updated, _ := m.Update(askUserRequestMsg{runID: 7, request: testAskUserRequest(), answer: func([]string) {}})
		next := updated.(model)
		updated, _ = next.Update(testKeyShift(tea.KeyTab))
		next = updated.(model)
		if next.permissionMode != agent.PermissionModeAuto {
			t.Fatalf("expected mode unchanged while ask_user prompt is up, got %q", next.permissionMode)
		}
		if next.pendingAskUser == nil {
			t.Fatal("expected ask_user prompt to remain pending")
		}
	})
	t.Run("picker", func(t *testing.T) {
		m := newModel(context.Background(), Options{
			ProviderName:   "openai",
			ModelName:      "gpt-4.1",
			Provider:       &fakeProvider{},
			PermissionMode: agent.PermissionModeAuto,
		})
		m.input.SetValue("/model")
		updated, _ := m.Update(testKey(tea.KeyEnter))
		next := updated.(model)
		if next.picker == nil {
			t.Skip("model picker unavailable in test environment")
		}
		updated, _ = next.Update(testKeyShift(tea.KeyTab))
		next = updated.(model)
		if next.permissionMode != agent.PermissionModeAuto {
			t.Fatalf("expected mode unchanged while picker is open, got %q", next.permissionMode)
		}
	})
}

func TestCtrlCExits(t *testing.T) {
	m := newModel(context.Background(), Options{})

	updated, cmd := m.Update(testKeyCtrl('c'))
	next := updated.(model)

	if !next.exiting {
		t.Fatal("expected Ctrl+C to mark model exiting")
	}
	if cmd == nil {
		t.Fatal("expected Ctrl+C to return quit command")
	}
}

func assertContains(t *testing.T, text any, want string) {
	t.Helper()

	content := plainRender(t, text)
	if !strings.Contains(content, want) {
		t.Fatalf("expected %q to contain %q", content, want)
	}
}

func assertNotContains(t *testing.T, text any, unwanted string) {
	t.Helper()

	content := plainRender(t, text)
	if strings.Contains(content, unwanted) {
		t.Fatalf("expected %q not to contain %q", content, unwanted)
	}
}

func transcriptContains(rows []transcriptRow, want string) bool {
	for _, row := range rows {
		if strings.Contains(row.text, want) {
			return true
		}
	}
	return false
}

func transcriptText(rows []transcriptRow) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.text)
		if row.detail != "" {
			parts = append(parts, row.detail)
		}
	}
	return strings.Join(parts, "\n")
}

func countTranscriptRows(rows []transcriptRow, kind rowKind) int {
	count := 0
	for _, row := range rows {
		if row.kind == kind {
			count++
		}
	}
	return count
}

func findTranscriptRow(rows []transcriptRow, kind rowKind) (transcriptRow, bool) {
	for _, row := range rows {
		if row.kind == kind {
			return row, true
		}
	}
	return transcriptRow{}, false
}

func transcriptHasMarkedModelEntry(rows []transcriptRow) bool {
	for _, row := range rows {
		for _, line := range strings.Split(row.text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "* ") && strings.Contains(trimmed, " (") {
				return true
			}
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func testPromptPermissionEvent() agent.PermissionEvent {
	return agent.PermissionEvent{
		ToolCallID:     "call_1",
		ToolName:       "write_file",
		Action:         agent.PermissionActionPrompt,
		Permission:     "prompt",
		PermissionMode: agent.PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		SideEffect:     "write",
		Reason:         "Creates or overwrites files.",
		Risk:           sandbox.Risk{Level: sandbox.RiskHigh},
	}
}

func testPromptPermissionRequest() agent.PermissionRequest {
	event := testPromptPermissionEvent()
	return agent.PermissionRequest{
		ToolCallID:     event.ToolCallID,
		ToolName:       event.ToolName,
		Action:         event.Action,
		Permission:     event.Permission,
		PermissionMode: event.PermissionMode,
		Autonomy:       event.Autonomy,
		SideEffect:     event.SideEffect,
		Reason:         event.Reason,
		Risk:           event.Risk,
	}
}

func testSessionStore(t *testing.T) *sessions.Store {
	t.Helper()

	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	return sessions.NewStore(sessions.StoreOptions{
		RootDir: t.TempDir(),
		Now: func() time.Time {
			now = now.Add(time.Minute)
			return now
		},
	})
}

func TestNextPermissionModeFoldsUnsafeToAsk(t *testing.T) {
	if got := nextPermissionMode(agent.PermissionModeAuto); got != agent.PermissionModeAsk {
		t.Fatalf("Auto -> %s, want Ask", got)
	}
	if got := nextPermissionMode(agent.PermissionModeAsk); got != agent.PermissionModeAuto {
		t.Fatalf("Ask -> %s, want Auto", got)
	}
	// Unsafe must fold to the STRICTER Ask, never Auto (toggling an Unsafe session
	// must not make it less strict).
	if got := nextPermissionMode(agent.PermissionModeUnsafe); got != agent.PermissionModeAsk {
		t.Fatalf("Unsafe -> %s, want Ask", got)
	}
}

func TestModelNotifierFocusAndCompletion(t *testing.T) {
	var buf bytes.Buffer
	m := model{notifier: notify.New(&buf, notify.Config{Mode: notify.ModeBell, FocusMode: notify.FocusUnfocused})}
	m.notifier.SetFocused(true)

	// Focused → completion under unfocused-mode is silent.
	m.notifier.Notify(notify.Completion, notify.DefaultMessage(notify.Completion))
	if buf.Len() != 0 {
		t.Fatalf("focused should be silent, got %q", buf.String())
	}

	// BlurMsg flips focus; completion now bells.
	updated, _ := m.Update(tea.BlurMsg{})
	m = updated.(model)
	m.notifier.Notify(notify.Completion, notify.DefaultMessage(notify.Completion))
	if buf.String() != "\x07" {
		t.Fatalf("unfocused should bell, got %q", buf.String())
	}

	// FocusMsg flips back.
	updated, _ = m.Update(tea.FocusMsg{})
	m = updated.(model)
	buf.Reset()
	m.notifier.Notify(notify.Completion, "x")
	if buf.Len() != 0 {
		t.Fatalf("refocused should be silent, got %q", buf.String())
	}
}

// TestWorkingVerbAdvancesOnStepEverySpinnerTicks locks in the cadence
// decision: the verb advances every WorkingWordsStepEvery spinner ticks
// (~1Hz at the 80ms spinner cadence), not every frame. The model owns the
// counter so the dumb Tick() ring stays unit-testable without a real
// spinner.
//
// We drive WorkingWordsStepEvery-1 ticks and assert the verb didn't
// change, then drive one more tick and assert it did. Then we drive
// another WorkingWordsStepEvery-1 ticks and assert the verb stayed put
// (i.e. the counter reset after the advance, not carried over). This
// catches a regression where someone removes the gate (revert to "every
// tick"), sets the gate to 1 (same effect), or forgets to reset the
// counter after the advance (so a follow-up advance lands one window
// early).
func TestWorkingVerbAdvancesOnStepEverySpinnerTicks(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	before := m.workingVerb.Current()

	// Drive (WorkingWordsStepEvery - 1) ticks. The verb must NOT advance.
	for i := 0; i < WorkingWordsStepEvery-1; i++ {
		updated, _ := m.Update(spinner.TickMsg{})
		m = updated.(model)
	}
	if got := m.workingVerb.Current(); got != before {
		t.Fatalf("after %d ticks verb = %q, want unchanged (%q)", WorkingWordsStepEvery-1, got, before)
	}

	// The (WorkingWordsStepEvery)th tick IS the advance.
	updated, _ := m.Update(spinner.TickMsg{})
	m = updated.(model)
	afterAdvance := m.workingVerb.Current()
	if afterAdvance == before {
		t.Fatalf("after %d ticks verb still = %q, want advance", WorkingWordsStepEvery, before)
	}

	// Drive another (WorkingWordsStepEvery - 1) ticks. The verb must NOT
	// advance again — the counter resets after each advance, not carries
	// over. A regression that omits the reset (so the next advance lands
	// one window early) would fail this assertion.
	for i := 0; i < WorkingWordsStepEvery-1; i++ {
		updated, _ := m.Update(spinner.TickMsg{})
		m = updated.(model)
	}
	if got := m.workingVerb.Current(); got != afterAdvance {
		t.Fatalf("after advance + %d ticks verb = %q, want unchanged from post-advance (%q) — counter didn't reset",
			WorkingWordsStepEvery-1, got, afterAdvance)
	}
}
