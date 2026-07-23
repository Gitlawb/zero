package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// fakeProvider streams a canned assistant message and ends the turn — enough to
// drive the real agent.Run loop without a live model.
type fakeProvider struct{ text string }

func (f fakeProvider) StreamCompletion(_ context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: f.text}
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	}()
	return ch, nil
}

func testDeps(t *testing.T) Deps {
	t.Helper()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	return Deps{
		ResolveConfig: func(_ string, o config.Overrides) (config.ResolvedConfig, error) {
			model := "fake-model"
			if o.Provider.Model != "" {
				model = o.Provider.Model
			}
			return config.ResolvedConfig{
				Provider: config.ProviderProfile{Name: "fake", Model: model},
				MaxTurns: 4,
			}, nil
		},
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return fakeProvider{text: "Hello from ZERO"}, nil
		},
		RunAgent: agent.Run,
		BuildWorkspace: func(string, config.ResolvedConfig) (*tools.Registry, *sandbox.Engine, error) {
			r := tools.NewRegistry()
			r.Register(tools.NewUpdatePlanTool())
			return r, nil, nil
		},
		ResolveWorkspaceRoot: func(cwd string) (string, error) { return cwd, nil },
		Store:                store,
		AgentInfo:            Implementation{Name: "zero", Version: "test"},
	}
}

// clientHarness wires a client Conn to an Agent over in-memory pipes and collects
// session/update text chunks.
type clientHarness struct {
	client  *Conn
	updates chan string
	stop    func()
}

func newHarness(t *testing.T, deps Deps) *clientHarness {
	t.Helper()
	ar, bw := io.Pipe() // agent -> client
	br, aw := io.Pipe() // client -> agent
	agentConn := NewConn(ar, aw)
	client := NewConn(br, bw)
	a := NewAgent(agentConn, deps)

	h := &clientHarness{client: client, updates: make(chan string, 128)}
	client.HandleNotify(MethodSessionUpdate, func(_ context.Context, params json.RawMessage) {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(params, &probe) != nil {
			return
		}
		if probe.Update.SessionUpdate == UpdateAgentMessageChunk {
			h.updates <- probe.Update.Content.Text
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = a.Serve(ctx) }()
	go func() { _ = client.Serve(ctx) }()
	h.stop = func() {
		cancel()
		_ = aw.Close()
		_ = bw.Close()
	}
	return h
}

func TestACPEndToEndPrompt(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// initialize
	var initRes InitializeResult
	if err := h.client.Call(ctx, MethodInitialize, InitializeParams{ProtocolVersion: ProtocolVersion}, &initRes); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if initRes.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version = %d", initRes.ProtocolVersion)
	}
	if !initRes.AgentCapabilities.LoadSession || !initRes.AgentCapabilities.PromptCapabilities.Image {
		t.Fatalf("unexpected capabilities: %+v", initRes.AgentCapabilities)
	}

	// session/new
	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if newRes.SessionID == "" {
		t.Fatal("session/new returned empty sessionId")
	}
	if newRes.Modes == nil || newRes.Modes.CurrentModeID != string(agent.PermissionModeAuto) {
		t.Fatalf("expected auto mode, got %+v", newRes.Modes)
	}
	if len(newRes.ConfigOptions) != 2 || newRes.ConfigOptions[0].ID != configIDModel || newRes.ConfigOptions[0].CurrentValue != "fake-model" {
		t.Fatalf("model config option = %+v, want fake-model fallback", newRes.ConfigOptions)
	}
	if newRes.ConfigOptions[1].ID != configIDMode || newRes.ConfigOptions[1].CurrentValue != string(agent.PermissionModeAuto) {
		t.Fatalf("mode config option = %+v", newRes.ConfigOptions[1])
	}

	// session/prompt
	var promptRes PromptResult
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{
		SessionID: newRes.SessionID,
		Prompt:    []ContentBlock{TextBlock("hi")},
	}, &promptRes); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if promptRes.StopReason != StopEndTurn {
		t.Fatalf("stopReason = %q, want %q", promptRes.StopReason, StopEndTurn)
	}

	// The streamed agent_message_chunk(s) should carry the assistant text.
	if got := drainText(t, h.updates); !strings.Contains(got, "Hello from ZERO") {
		t.Fatalf("streamed text = %q, want it to contain the assistant message", got)
	}
}

func TestACPModelConfigOptionsCatalogSelectionAndLoad(t *testing.T) {
	deps := testDeps(t)
	deps.ResolveConfig = func(_ string, o config.Overrides) (config.ResolvedConfig, error) {
		model := "gpt-5.5"
		if o.Provider.Model != "" {
			model = o.Provider.Model
		}
		return config.ResolvedConfig{Provider: config.ProviderProfile{
			Name: "ChatGPT", CatalogID: "chatgpt", Model: model,
		}}, nil
	}
	deps.DiscoverModels = func(_ context.Context, _ config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		return []providermodeldiscovery.Model{
			{ID: " gpt-5.4-mini ", Description: "Fast"},
			{ID: "gpt-5.5", Description: "duplicate configured model"},
		}, nil
	}
	h := newHarness(t, deps)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var created NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir()}, &created); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	option := created.ConfigOptions[0]
	if option.CurrentValue != "gpt-5.5" || len(option.Options) < 2 {
		t.Fatalf("new model option = %+v", option)
	}
	for _, choice := range option.Options {
		if choice.Name != choice.Value {
			t.Fatalf("model choice name = %q, want model id %q", choice.Name, choice.Value)
		}
	}
	var selected SetSessionConfigOptionResult
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{
		SessionID: created.SessionID, ConfigID: configIDModel, Value: "  gpt-5.4-mini  ",
	}, &selected); err != nil {
		t.Fatalf("set_config_option: %v", err)
	}
	if got := selected.ConfigOptions[0].CurrentValue; got != "gpt-5.4-mini" {
		t.Fatalf("selected model = %q", got)
	}
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{
		SessionID: created.SessionID, ConfigID: configIDModel, Value: "not-advertised",
	}, &SetSessionConfigOptionResult{}); err == nil {
		t.Fatal("unknown standard model selection was accepted")
	}
	h.stop()
	h = newHarness(t, deps)
	defer h.stop()
	var loaded LoadSessionResult
	if err := h.client.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: created.SessionID}, &loaded); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	if len(loaded.ConfigOptions) != 2 || loaded.ConfigOptions[0].CurrentValue != "gpt-5.4-mini" {
		t.Fatalf("load model option = %+v", loaded.ConfigOptions)
	}
	if loaded.ConfigOptions[1].CurrentValue != string(agent.PermissionModeAuto) {
		t.Fatalf("load mode option = %+v", loaded.ConfigOptions[1])
	}
}

func TestACPModelDiscoveryFailureUsesConfiguredFallbackOnly(t *testing.T) {
	deps := testDeps(t)
	deps.ResolveConfig = func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
		return config.ResolvedConfig{Provider: config.ProviderProfile{
			Name: "ChatGPT", CatalogID: "chatgpt", Model: " configured-model ",
		}}, nil
	}
	deps.DiscoverModels = func(context.Context, config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		return nil, fmt.Errorf("discovery unavailable")
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var created NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir()}, &created); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	option := created.ConfigOptions[0]
	if option.CurrentValue != "configured-model" || len(option.Options) != 1 || option.Options[0].Value != "configured-model" {
		t.Fatalf("fallback model option = %+v", option)
	}
}

func TestACPCustomProviderAllowsUnadvertisedModel(t *testing.T) {
	deps := testDeps(t)
	deps.ResolveConfig = func(_ string, o config.Overrides) (config.ResolvedConfig, error) {
		model := "configured-model"
		if o.Provider.Model != "" {
			model = o.Provider.Model
		}
		return config.ResolvedConfig{Provider: config.ProviderProfile{
			Name: "Custom", CatalogID: "custom-openai-compatible", Model: model,
		}}, nil
	}
	h := newHarness(t, deps)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var created NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir()}, &created); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var selected SetSessionConfigOptionResult
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{
		SessionID: created.SessionID, ConfigID: configIDModel, Value: " vendor/model ",
	}, &selected); err != nil {
		t.Fatalf("set custom model: %v", err)
	}
	if got := selected.ConfigOptions[0].CurrentValue; got != "vendor/model" {
		t.Fatalf("custom model = %q", got)
	}
	h.stop()
	h = newHarness(t, deps)
	defer h.stop()
	var loaded LoadSessionResult
	if err := h.client.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: created.SessionID}, &loaded); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	option := loaded.ConfigOptions[0]
	if option.CurrentValue != "vendor/model" || !modelChoiceExists(option.Options, "vendor/model") {
		t.Fatalf("loaded custom model option = %+v", option)
	}
}

func TestACPModelDiscoveryFiltersProviderIncompatibleModels(t *testing.T) {
	a := &Agent{deps: Deps{
		ResolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{Provider: config.ProviderProfile{
				CatalogID: "opencode-go-anthropic-compatible", Model: "minimax-m3",
			}}, nil
		},
		DiscoverModels: func(context.Context, config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return []providermodeldiscovery.Model{{ID: "qwen3.7-plus"}, {ID: "claude-sonnet-4.5"}}, nil
		},
	}}
	_, options, restricted, err := a.resolveModelChoices(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !restricted || !modelChoiceExists(options, "qwen3.7-plus") || modelChoiceExists(options, "claude-sonnet-4.5") {
		t.Fatalf("filtered model options = %+v, restricted=%v", options, restricted)
	}
}

func TestACPModelDiscoveryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &Agent{deps: Deps{
		ResolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{Provider: config.ProviderProfile{Model: "configured-model"}}, nil
		},
		DiscoverModels: func(context.Context, config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return nil, context.Canceled
		},
	}}
	if _, _, _, err := a.resolveModelChoices(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("resolve error = %v, want context canceled", err)
	}
}

func TestACPConfigOptionWireSchema(t *testing.T) {
	b, err := json.Marshal(SessionConfigOption{
		ID: "model", Name: "Model", Description: "desc", Category: "model",
		Type: configOptionTypeSelect, CurrentValue: "m1",
		Options: []SessionConfigOptionValue{{Value: "m1", Name: "m1", Description: "choice"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "category", "currentValue", "options"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("wire field %q absent: %s", key, b)
		}
	}
	if _, ok := wire["value"]; ok {
		t.Errorf("obsolete option value field present: %s", b)
	}
	if _, ok := wire["values"]; ok {
		t.Errorf("obsolete values field present: %s", b)
	}
	choice := wire["options"].([]any)[0].(map[string]any)
	if choice["value"] != "m1" {
		t.Errorf("options[].value = %#v", choice["value"])
	}
}

func TestACPUnknownSessionPromptErrors(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: "nope", Prompt: []ContentBlock{TextBlock("x")}}, &PromptResult{})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestACPSetModeUpdatesSession(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	// auto/ask are accepted.
	if err := h.client.Call(ctx, MethodSessionSetMode, SetSessionModeParams{SessionID: newRes.SessionID, ModeID: string(agent.PermissionModeAsk)}, &SetSessionModeResult{}); err != nil {
		t.Fatalf("set_mode ask: %v", err)
	}
	var configured SetSessionConfigOptionResult
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{
		SessionID: newRes.SessionID, ConfigID: configIDMode, Value: string(agent.PermissionModeAuto),
	}, &configured); err != nil {
		t.Fatalf("set_config_option mode: %v", err)
	}
	if got := configured.ConfigOptions[1].CurrentValue; got != string(agent.PermissionModeAuto) {
		t.Fatalf("configured mode = %q", got)
	}
	// Unsafe must be rejected over ACP — a client can't self-grant no-prompt host access.
	if err := h.client.Call(ctx, MethodSessionSetMode, SetSessionModeParams{SessionID: newRes.SessionID, ModeID: string(agent.PermissionModeUnsafe)}, &SetSessionModeResult{}); err == nil {
		t.Fatal("expected Unsafe mode to be rejected over ACP")
	}
	// An unknown mode must be rejected.
	if err := h.client.Call(ctx, MethodSessionSetMode, SetSessionModeParams{SessionID: newRes.SessionID, ModeID: "bogus"}, &SetSessionModeResult{}); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

// TestACPRunTurnWiresSandboxAndScopedRegistry proves the sandbox engine and the
// scoped registry from BuildWorkspace actually reach agent.Options — i.e. ACP
// shell tools run confined, not unconfined on the host.
func TestACPRunTurnWiresSandboxAndScopedRegistry(t *testing.T) {
	deps := testDeps(t)
	reg := tools.NewRegistry()
	reg.Register(tools.NewUpdatePlanTool())
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: t.TempDir()})
	deps.BuildWorkspace = func(string, config.ResolvedConfig) (*tools.Registry, *sandbox.Engine, error) {
		return reg, engine, nil
	}
	var captured agent.Options
	deps.RunAgent = func(_ context.Context, _ string, _ zeroruntime.Provider, opts agent.Options) (agent.Result, error) {
		captured = opts
		return agent.Result{FinalAnswer: "ok"}, nil
	}

	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if captured.Sandbox != engine {
		t.Fatal("sandbox engine was not wired into agent.Options (shell tools would run unconfined)")
	}
	if captured.Registry != reg {
		t.Fatal("scoped registry was not wired into agent.Options")
	}
}

// TestACPRejectsInvalidCwd confirms session/new fails when the workspace root
// resolver rejects the client cwd (e.g. filesystem root).
func TestACPRejectsInvalidCwd(t *testing.T) {
	deps := testDeps(t)
	deps.ResolveWorkspaceRoot = func(string) (string, error) {
		return "", fmt.Errorf("cwd must not be the filesystem root")
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: "/", McpServers: []McpServer{}}, &NewSessionResult{}); err == nil {
		t.Fatal("expected session/new to reject an invalid cwd")
	}
}

func TestACPPromptWarnsWhenTurnPersistenceFails(t *testing.T) {
	deps := testDeps(t)
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	metadataPath := filepath.Join(deps.Store.RootDir, newRes.SessionID, sessions.MetadataFile)
	if err := os.Remove(metadataPath); err != nil {
		t.Fatalf("remove metadata: %v", err)
	}

	var promptRes PromptResult
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{
		SessionID: newRes.SessionID,
		Prompt:    []ContentBlock{TextBlock("hi")},
	}, &promptRes); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if promptRes.StopReason != StopEndTurn {
		t.Fatalf("stopReason = %q, want %q", promptRes.StopReason, StopEndTurn)
	}
	got := drainTextUntil(t, h.updates, func(text string) bool {
		return strings.Contains(text, "Hello from ZERO") &&
			strings.Contains(text, "Could not save session history")
	})
	if !strings.Contains(got, "Could not save session history") {
		t.Fatalf("streamed text = %q, want persistence warning", got)
	}
}

func TestACPLoadWarnsWhenHistoryReadFails(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	meta, err := deps.Store.Create(sessions.CreateInput{Title: "ACP session", Cwd: cwd})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	eventsPath := filepath.Join(deps.Store.RootDir, meta.SessionID, sessions.EventsFile)
	if err := os.WriteFile(eventsPath, []byte("{bad json}\n"), 0o600); err != nil {
		t.Fatalf("write corrupt events: %v", err)
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: meta.SessionID, Cwd: cwd, McpServers: []McpServer{}}, &LoadSessionResult{}); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	got := drainTextUntil(t, h.updates, func(text string) bool {
		return strings.Contains(text, "Could not load session history")
	})
	if !strings.Contains(got, "Could not load session history") {
		t.Fatalf("streamed text = %q, want load warning", got)
	}
}

// drainText collects streamed chunks for a short window and concatenates them.
func drainText(t *testing.T, ch <-chan string) string {
	t.Helper()
	return drainTextUntil(t, ch, func(text string) bool {
		return strings.Contains(text, "Hello from ZERO")
	})
}

func drainTextUntil(t *testing.T, ch <-chan string, done func(string) bool) string {
	t.Helper()
	var b strings.Builder
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-ch:
			b.WriteString(s)
			if done(b.String()) {
				return b.String()
			}
		case <-deadline:
			return b.String()
		}
	}
}
