package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	client        *Conn
	updates       chan string
	notifications chan ContentChunk
	stop          func()
}

func newHarness(t *testing.T, deps Deps) *clientHarness {
	t.Helper()
	ar, bw := io.Pipe() // agent -> client
	br, aw := io.Pipe() // client -> agent
	agentConn := NewConn(ar, aw)
	client := NewConn(br, bw)
	a := NewAgent(agentConn, deps)

	h := &clientHarness{client: client, updates: make(chan string, 128), notifications: make(chan ContentChunk, 128)}
	client.HandleNotify(MethodSessionUpdate, func(_ context.Context, params json.RawMessage) {
		var probe struct {
			Update ContentChunk `json:"update"`
		}
		if json.Unmarshal(params, &probe) != nil {
			return
		}
		if probe.Update.SessionUpdate == UpdateAgentMessageChunk || probe.Update.SessionUpdate == UpdateUserMessageChunk {
			h.notifications <- probe.Update
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
	if !initRes.AgentCapabilities.LoadSession || !initRes.AgentCapabilities.PromptCapabilities.Image ||
		initRes.AgentCapabilities.SessionCapabilities == nil || initRes.AgentCapabilities.SessionCapabilities.List == nil || initRes.AgentCapabilities.SessionCapabilities.Resume == nil {
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

func TestACPListsOnlyResumableSessionMetadata(t *testing.T) {
	deps := testDeps(t)
	deps.ResolveWorkspaceRoot = func(cwd string) (string, error) { return filepath.Clean(cwd), nil }
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	for _, input := range []sessions.CreateInput{
		{SessionID: "desktop-a", Title: "First", Cwd: workspaceA, ModelID: "model-a"},
		{SessionID: "desktop-b", Title: "Second", Cwd: workspaceB, ModelID: "model-b"},
		{SessionID: "child-run", SessionKind: sessions.SessionKindChild, Title: "Internal child", Cwd: workspaceA},
	} {
		if _, err := deps.Store.Create(input); err != nil {
			t.Fatalf("create %s: %v", input.SessionID, err)
		}
	}

	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var all ListSessionsResult
	if err := h.client.Call(ctx, MethodSessionList, ListSessionsParams{}, &all); err != nil {
		t.Fatalf("session/list: %v", err)
	}
	if len(all.Sessions) != 2 {
		t.Fatalf("all sessions = %+v, want two resumable sessions", all.Sessions)
	}
	byID := make(map[string]SessionInfo, len(all.Sessions))
	for _, item := range all.Sessions {
		byID[item.SessionID] = item
	}
	if _, found := byID["child-run"]; found {
		t.Fatal("agent-owned child session leaked into the desktop session picker")
	}
	if got := byID["desktop-a"]; got.Title != "First" || got.Cwd != workspaceA || got.Meta == nil || got.Meta.ModelID != "model-a" || got.Meta.CreatedAt == "" || got.UpdatedAt == "" {
		t.Fatalf("desktop-a summary = %+v", got)
	}

	var filtered ListSessionsResult
	if err := h.client.Call(ctx, MethodSessionList, ListSessionsParams{Cwd: workspaceB}, &filtered); err != nil {
		t.Fatalf("filtered session/list: %v", err)
	}
	if len(filtered.Sessions) != 1 || filtered.Sessions[0].SessionID != "desktop-b" {
		t.Fatalf("filtered sessions = %+v, want only desktop-b", filtered.Sessions)
	}

	var equivalent ListSessionsResult
	equivalentPath := workspaceB + string(os.PathSeparator) + "."
	if err := h.client.Call(ctx, MethodSessionList, ListSessionsParams{Cwd: equivalentPath}, &equivalent); err != nil {
		t.Fatalf("equivalent-path session/list: %v", err)
	}
	if len(equivalent.Sessions) != 1 || equivalent.Sessions[0].SessionID != "desktop-b" {
		t.Fatalf("equivalent-path sessions = %+v, want only desktop-b", equivalent.Sessions)
	}

	err := h.client.Call(ctx, MethodSessionList, ListSessionsParams{Cursor: "not-issued"}, &ListSessionsResult{})
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr.Code != codeInvalidParams {
		t.Fatalf("invalid cursor error = %v, want invalid params", err)
	}
}

func TestACPLoadReplaysHistoryAndResumeDoesNot(t *testing.T) {
	deps := testDeps(t)
	workspace := t.TempDir()
	created, err := deps.Store.Create(sessions.CreateInput{SessionID: "replay-session", Title: "Replay", Cwd: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Store.AppendEvents(created.SessionID, []sessions.AppendEventInput{
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "first user"}},
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "assistant", "content": "first answer"}},
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "second user"}},
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	loader := newHarness(t, deps)
	var loaded LoadSessionResult
	if err := loader.client.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: created.SessionID, Cwd: workspace, McpServers: []McpServer{}}, &loaded); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	wantKinds := []string{UpdateUserMessageChunk, UpdateAgentMessageChunk, UpdateUserMessageChunk}
	wantText := []string{"first user", "first answer", "second user"}
	seenIDs := map[string]bool{}
	firstLoadIDs := make([]string, 0, len(wantKinds))
	for i := range wantKinds {
		select {
		case update := <-loader.notifications:
			if update.SessionUpdate != wantKinds[i] || update.Content.Text != wantText[i] {
				t.Fatalf("history update %d = %+v", i, update)
			}
			if update.MessageID == "" || seenIDs[update.MessageID] {
				t.Fatalf("history update %d has missing/duplicate message id %q", i, update.MessageID)
			}
			seenIDs[update.MessageID] = true
			firstLoadIDs = append(firstLoadIDs, update.MessageID)
		case <-ctx.Done():
			t.Fatalf("history update %d was not replayed", i)
		}
	}
	loader.stop()
	secondLoader := newHarness(t, deps)
	if err := secondLoader.client.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: created.SessionID, Cwd: workspace, McpServers: []McpServer{}}, &LoadSessionResult{}); err != nil {
		t.Fatalf("second session/load: %v", err)
	}
	for i, wantID := range firstLoadIDs {
		select {
		case update := <-secondLoader.notifications:
			if update.MessageID != wantID {
				t.Fatalf("second load message id %d = %q, want stable %q", i, update.MessageID, wantID)
			}
		case <-ctx.Done():
			t.Fatalf("second load history update %d was not replayed", i)
		}
	}
	secondLoader.stop()

	resumer := newHarness(t, deps)
	defer resumer.stop()
	if err := resumer.client.Call(ctx, MethodSessionResume, ResumeSessionParams{SessionID: created.SessionID, Cwd: workspace, McpServers: []McpServer{}}, &ResumeSessionResult{}); err != nil {
		t.Fatalf("session/resume: %v", err)
	}
	select {
	case update := <-resumer.notifications:
		t.Fatalf("session/resume replayed history: %+v", update)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestACPLoadAndResumeStayBoundToThePersistedWorkspace(t *testing.T) {
	deps := testDeps(t)
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	created, err := deps.Store.Create(sessions.CreateInput{SessionID: "workspace-bound", Cwd: workspaceA})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, method := range []string{MethodSessionLoad, MethodSessionResume} {
		err := h.client.Call(ctx, method, LoadSessionParams{SessionID: created.SessionID, Cwd: workspaceB, McpServers: []McpServer{}}, &LoadSessionResult{})
		var rpcErr *rpcError
		if !errors.As(err, &rpcErr) || rpcErr.Code != codeInvalidParams || !strings.Contains(rpcErr.Message, "persisted workspace") {
			t.Fatalf("%s with mismatched cwd error = %v, want persisted-workspace invalid params", method, err)
		}
	}

	missing, err := deps.Store.Create(sessions.CreateInput{SessionID: "workspace-missing"})
	if err != nil {
		t.Fatal(err)
	}
	err = h.client.Call(ctx, MethodSessionResume, ResumeSessionParams{SessionID: missing.SessionID, Cwd: workspaceA, McpServers: []McpServer{}}, &ResumeSessionResult{})
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr.Code != codeInvalidParams || !strings.Contains(rpcErr.Message, "no persisted workspace") {
		t.Fatalf("resume with missing persisted cwd error = %v, want invalid params", err)
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
	// Plan is accepted: it only narrows capability (read-only), so unlike Unsafe
	// there is no elevation risk in letting a client select it.
	if err := h.client.Call(ctx, MethodSessionSetMode, SetSessionModeParams{SessionID: newRes.SessionID, ModeID: string(agent.PermissionModePlan)}, &SetSessionModeResult{}); err != nil {
		t.Fatalf("set_mode plan: %v", err)
	}
	// Configuration path is a separate contract from MethodSessionSetMode: the
	// mode option is advertised via configOptions and applied by handleSetConfigOption.
	var planConfigured SetSessionConfigOptionResult
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{
		SessionID: newRes.SessionID, ConfigID: configIDMode, Value: string(agent.PermissionModePlan),
	}, &planConfigured); err != nil {
		t.Fatalf("set_config_option plan: %v", err)
	}
	if got := planConfigured.ConfigOptions[1].CurrentValue; got != string(agent.PermissionModePlan) {
		t.Fatalf("configured mode after plan = %q, want plan", got)
	}
	hasPlanOption := false
	for _, opt := range planConfigured.ConfigOptions[1].Options {
		if opt.Value == string(agent.PermissionModePlan) {
			hasPlanOption = true
			break
		}
	}
	if !hasPlanOption {
		t.Fatalf("config mode options missing plan: %#v", planConfigured.ConfigOptions[1].Options)
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

// TestACPPlanModeWiresPermissionModeIntoAgentOptions confirms selecting "plan"
// over ACP actually reaches agent.Options.PermissionMode for the next turn —
// the same gap this test's TUI counterpart covers for /plan on.
func TestACPPlanModeWiresPermissionModeIntoAgentOptions(t *testing.T) {
	deps := testDeps(t)
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
	if err := h.client.Call(ctx, MethodSessionSetMode, SetSessionModeParams{SessionID: newRes.SessionID, ModeID: string(agent.PermissionModePlan)}, &SetSessionModeResult{}); err != nil {
		t.Fatalf("set_mode plan: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("plan it out")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if captured.PermissionMode != agent.PermissionModePlan {
		t.Fatalf("agent.Options.PermissionMode = %q, want plan", captured.PermissionMode)
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

// normalisingResolver reproduces what ResolveWorkspaceRoot actually does — abs,
// Clean, and a stat that the path exists — WITHOUT resolving symlinks or folding
// case, which is the behaviour that makes two spellings of one directory produce
// two different roots.
//
// The package's own testDeps resolver is the identity function. Under it the
// workspace guard degenerates to "are these two strings different", fed two
// unrelated temp directories, so it can only ever answer yes: the rejection
// direction is pinned and the acceptance direction is asserted nowhere.
func normalisingResolver(t *testing.T) func(string) (string, error) {
	t.Helper()
	return func(cwd string) (string, error) {
		absolute, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		absolute = filepath.Clean(absolute)
		info, err := os.Stat(absolute)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", errors.New("workspace is not a directory")
		}
		return absolute, nil
	}
}

// ONE DIRECTORY UNDER TWO SPELLINGS IS ONE WORKSPACE.
//
// A session persisted from the TUI has to stay resumable from an editor holding
// a different spelling of the same project folder — the two processes most
// likely to disagree about spelling, and the case this feature exists for. It
// failed closed, so it blocked legitimate resumes rather than admitting foreign
// ones, but session/list filtered by the other spelling returned nothing, which
// makes it an invisible failure rather than a reported one.
func TestACPResumesAcrossTwoSpellingsOfOneWorkspace(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("cannot create a directory alias here: %v", err)
	}
	// The premise: the resolver really does produce two different strings.
	resolve := normalisingResolver(t)
	realRoot, err := resolve(real)
	if err != nil {
		t.Fatal(err)
	}
	aliasRoot, err := resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	if realRoot == aliasRoot {
		t.Skipf("this filesystem folds the alias away (%q == %q); the guard cannot be exercised", realRoot, aliasRoot)
	}

	deps := testDeps(t)
	deps.ResolveWorkspaceRoot = resolve
	created, err := deps.Store.Create(sessions.CreateInput{SessionID: "aliased-workspace", Cwd: real})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ACCEPTANCE: resume and load under the OTHER spelling must both work.
	for _, method := range []string{MethodSessionLoad, MethodSessionResume} {
		var result LoadSessionResult
		if err := h.client.Call(ctx, method, LoadSessionParams{SessionID: created.SessionID, Cwd: alias, McpServers: []McpServer{}}, &result); err != nil {
			t.Errorf("%s under an alias of the persisted workspace failed: %v", method, err)
		}
	}

	// And session/list filtered by the alias must still find it.
	var listed ListSessionsResult
	if err := h.client.Call(ctx, MethodSessionList, ListSessionsParams{Cwd: alias}, &listed); err != nil {
		t.Fatalf("session/list: %v", err)
	}
	found := false
	for _, item := range listed.Sessions {
		if item.SessionID == created.SessionID {
			found = true
		}
	}
	if !found {
		t.Errorf("session/list filtered by an alias of its own workspace returned %d sessions without it", len(listed.Sessions))
	}

	// REJECTION still holds: a genuinely different directory is refused.
	other := t.TempDir()
	err = h.client.Call(ctx, MethodSessionResume, ResumeSessionParams{SessionID: created.SessionID, Cwd: other, McpServers: []McpServer{}}, &ResumeSessionResult{})
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr.Code != codeInvalidParams {
		t.Errorf("resume from an unrelated workspace = %v, want invalid params", err)
	}
}

// THE WIRE KEYS ARE AN EXTERNAL CONTRACT, not internal names.
//
// Nothing pinned them, so renaming a Go field — or dropping an omitempty —
// would break every client and leave the suite green. These are the keys this
// feature adds to the protocol; a change here is a change to what editors
// consume.
func TestSessionWireKeysAreStable(t *testing.T) {
	marshalled := func(v any) map[string]any {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	has := func(where string, got map[string]any, want ...string) {
		t.Helper()
		for _, key := range want {
			if _, ok := got[key]; !ok {
				t.Errorf("%s is missing the wire key %q; got %v", where, key, keysOf(got))
			}
		}
	}

	has("SessionInfo", marshalled(SessionInfo{
		SessionID: "s1", Cwd: "/w", Title: "t", UpdatedAt: "now",
		Meta: &SessionInfoMeta{ModelID: "m", CreatedAt: "then"},
	}), "sessionId", "cwd", "title", "updatedAt", "_meta")

	has("SessionInfoMeta", marshalled(SessionInfoMeta{ModelID: "m", CreatedAt: "then"}),
		"modelId", "createdAt")

	has("ListSessionsResult", marshalled(ListSessionsResult{
		Sessions: []SessionInfo{}, NextCursor: "c",
	}), "sessions", "nextCursor")

	has("ListSessionsParams", marshalled(ListSessionsParams{Cwd: "/w", Cursor: "c"}),
		"cwd", "cursor")

	// sessionCapabilities is omitempty, so it must appear when SET — a client
	// discovers list/resume support through it.
	has("AgentCapabilities", marshalled(AgentCapabilities{
		LoadSession:         true,
		SessionCapabilities: &SessionCapabilities{List: &struct{}{}, Resume: &struct{}{}},
	}), "loadSession", "promptCapabilities", "sessionCapabilities")

	capabilities := marshalled(SessionCapabilities{List: &struct{}{}, Resume: &struct{}{}})
	has("SessionCapabilities", capabilities, "list", "resume")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
