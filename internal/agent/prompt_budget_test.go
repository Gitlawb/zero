package agent

import (
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

// These ceilings are a deliberate ratchet on Zero's fixed per-turn overhead — the
// system prompt and the eager tool schemas that ride on EVERY request. They are
// set ~10% above the current measured cost, using ApproxTextTokens (the same
// estimate the compaction loop and /context use, ~non-whitespace-bytes/4). A change
// that pushes either past its ceiling must be justified (and the ceiling raised
// deliberately) or trimmed — the per-turn floor should not creep up silently.
//
// Measured baselines (2026-07): base system prompt ~3160 tokens; the tools a normal
// (auto permission-mode) interactive turn sends ~3230 tokens. The tool figure is
// the auto-mode advertised set, including exec_command: auto mode must expose both
// process creation and write_stdin process interaction or the latter is unusable
// without a session id. Higher-risk tools that do not opt into auto remain excluded.
const (
	maxBaseSystemPromptTokens = 3500
	// Raised deliberately from 3550 on 2026-08-08. view_image (#843) took the set
	// to 3588, and #867 trimmed it back to 3578: still 28 tokens over.
	//
	// The overrun is a rounding error rather than a regression. view_image costs
	// 82 tokens, the second cheapest tool in the set, against exec_command's 909
	// and request_permissions' 397; it is simply the one that crossed the line.
	// Raised rather than paid down because the alternative was trimming a
	// description to claw back 28 tokens, degrading a tool's usability to satisfy
	// a line drawn against a 2026-07 measurement that predates two tools.
	//
	// The ratchet did its job: it caught the creep and forced a decision instead
	// of a drift. That is the point of it, so keep raising it deliberately rather
	// than reflexively.
	maxEagerToolSchemaTokens = 3650
)

func TestSystemPromptTokenBudget(t *testing.T) {
	// Minimal render: a model is set (so the session block renders) but no Cwd, so
	// the workspace map, project guidelines, and repo map are excluded — this is the
	// fixed base every session pays regardless of the workspace it runs in.
	prompt := buildSystemPrompt(Options{Model: "claude-opus-4-8"})
	got := ApproxTextTokens(prompt)
	t.Logf("base system prompt: %d tokens (%d bytes)", got, len(prompt))
	if got > maxBaseSystemPromptTokens {
		t.Fatalf("base system prompt is %d tokens, over the %d ceiling — trim it or raise the ceiling deliberately", got, maxBaseSystemPromptTokens)
	}
}

func TestEagerToolSchemaTokenBudget(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreToolsScoped(t.TempDir(), nil) {
		registry.Register(tool)
	}
	// Options{} keeps DeferThreshold at 0, so deferral is inactive and every core
	// tool is exposed eagerly — exactly what a plugin-free session sends each turn.
	exposed, _ := partitionTools(registry, PermissionModeAuto, Options{}, map[string]bool{})
	got := estimateToolDefTokens(exposed)
	t.Logf("eager core tool schemas: %d tokens across %d tools", got, len(exposed))
	if got > maxEagerToolSchemaTokens {
		// NOT "defer a tool": this test pins DeferThreshold at 0, so marking a
		// tool deferred leaves it exposed here and changes nothing. Deferral is
		// still worth doing for real sessions, it just cannot move this number.
		// The levers that do are a smaller schema, one fewer core tool, or a
		// deliberate raise.
		t.Fatalf("eager tool schemas are %d tokens, over the %d ceiling — trim a schema, drop a core tool, or raise the ceiling deliberately (deferring will NOT help: this test disables deferral)", got, maxEagerToolSchemaTokens)
	}
}

func TestAgentAdvertisesPatchInsteadOfAmbiguousStringReplacement(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreToolsScoped(t.TempDir(), nil) {
		registry.Register(tool)
	}
	exposed, _ := partitionTools(registry, PermissionModeAsk, Options{}, map[string]bool{})
	names := make(map[string]bool, len(exposed))
	for _, definition := range exposed {
		names[definition.Name] = true
	}
	if !names["apply_patch"] {
		t.Fatal("agent must retain apply_patch for existing-file changes")
	}
	if names["edit_file"] {
		t.Fatal("agent must not receive the ambiguous string-replacement tool")
	}
}
