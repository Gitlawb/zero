package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
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

// drainText collects streamed chunks for a short window and concatenates them.
func drainText(t *testing.T, ch <-chan string) string {
	t.Helper()
	var b strings.Builder
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-ch:
			b.WriteString(s)
			if strings.Contains(b.String(), "Hello from ZERO") {
				return b.String()
			}
		case <-deadline:
			return b.String()
		}
	}
}
