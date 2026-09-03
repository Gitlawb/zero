package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/tools"
	zeroruntime "github.com/Gitlawb/zero/internal/zeroruntime"
)

// A SUCCESSFUL beforeTool HOOK'S OUTPUT REACHES THE MODEL.
//
// executeToolCall used to read the beforeTool outcome only when Blocked was
// true, so a hook that ran fine and printed something — an enforcement notice
// saying it had run under the weakened DenyRead token, for instance — landed in
// the audit record and on no surface the model or the operator could see. Only
// vetoes and afterTool feedback got out.
//
// Driven through the real Run loop with a real hook process, and asserted on
// what the PROVIDER actually received on the next turn, because that is the
// delivery that matters. A unit test on the joining helper cannot see whether
// the loop captures the messages at all.
func TestSuccessfulBeforeToolHookOutputReachesTheModel(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		goRoot := runtime.GOROOT()
		if goRoot == "" {
			t.Skip("go binary unavailable for the hook command")
		}
		goBinary = filepath.Join(goRoot, "bin", "go")
		if runtime.GOOS == "windows" {
			goBinary += ".exe"
		}
	}
	audit, err := hooks.NewAuditStore(hooks.AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
	if err != nil {
		t.Fatalf("NewAuditStore: %v", err)
	}
	// Exits 0 and prints to stdout: a hook that permits the call and still has
	// something to say.
	dispatcher := hooks.NewDispatcher(hooks.DispatcherOptions{
		Config: hooks.Config{
			Enabled: true,
			Hooks: []hooks.Definition{
				{ID: "zero.before-tool", Event: hooks.EventBeforeTool, Matcher: "read_file", Command: goBinary, Args: []string{"version"}, Enabled: true},
			},
		},
		Audit: audit,
	})

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt"}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
			{Type: zeroruntime.StreamEventDone},
		},
		{
			{Type: zeroruntime.StreamEventText, Content: "read it"},
			{Type: zeroruntime.StreamEventDone},
		},
	}}

	if _, err := Run(context.Background(), "read the notes", provider, Options{
		SessionID:    "session-hook",
		Cwd:          root,
		Registry:     registry,
		ProviderName: "test-provider",
		Model:        "test-model",
		Hooks:        dispatcher,
		MaxTurns:     2,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The tool itself ran, so the setup is the one under test rather than a
	// blocked call that never reached the tool.
	if !someRequestContains(provider.requests, "hello") {
		t.Fatalf("SETUP INVALID: the tool result never reached the model, so nothing was delivered to check")
	}
	// go version prints "go version ..." on stdout; that is the hook's message.
	if !someRequestContains(provider.requests, "go version") {
		t.Fatal("a successful beforeTool hook produced output that never reached the model")
	}
	if !someRequestContains(provider.requests, "Hook output:") {
		t.Error("the hook output was delivered without the header the model uses to recognise it")
	}
}
