package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestCoreReadOnlyToolsExposeSafeMetadata(t *testing.T) {
	toolset := CoreReadOnlyTools(t.TempDir())
	if len(toolset) != 9 {
		t.Fatalf("expected 9 core read-only tools, got %d", len(toolset))
	}

	seen := map[string]bool{}
	for _, tool := range toolset {
		seen[tool.Name()] = true
		if tool.Name() == "" {
			t.Fatalf("tool has empty name")
		}
		if tool.Description() == "" {
			t.Fatalf("%s has empty description", tool.Name())
		}
		wantSideEffect := SideEffectRead
		if tool.Name() == RequestPermissionsToolName {
			wantSideEffect = SideEffectNone
		}
		if tool.Safety().SideEffect != wantSideEffect {
			t.Fatalf("%s side effect = %s, want %s", tool.Name(), tool.Safety().SideEffect, wantSideEffect)
		}
		if tool.Safety().Permission != PermissionAllow {
			t.Fatalf("%s permission = %s, want allow", tool.Name(), tool.Safety().Permission)
		}
		if tool.Safety().Reason == "" {
			t.Fatalf("%s has empty safety reason", tool.Name())
		}

		schema := tool.Parameters()
		if schema.Type != "object" {
			t.Fatalf("%s schema type = %s, want object", tool.Name(), schema.Type)
		}
		if schema.Properties == nil {
			t.Fatalf("%s schema properties are nil", tool.Name())
		}
		if schema.AdditionalProperties {
			t.Fatalf("%s schema should disallow additional properties", tool.Name())
		}
	}
	if !seen[RequestPermissionsToolName] {
		t.Fatalf("expected %s in core read-only tools", RequestPermissionsToolName)
	}
}

func TestCoreNetworkToolsExposeSafetyMetadata(t *testing.T) {
	// web_search is only registered when a backend is configured.
	t.Setenv("ZERO_WEBSEARCH_BASE_URL", "https://search.example/api")
	byName := map[string]Tool{}
	for _, tool := range CoreNetworkTools() {
		byName[tool.Name()] = tool
		safety := tool.Safety()
		if safety.SideEffect != SideEffectNetwork {
			t.Fatalf("network tool %q has unexpected safety metadata: %#v", tool.Name(), safety)
		}
	}

	// web_fetch retrieves a known URL; its key parameter is "url".
	fetch, ok := byName["web_fetch"]
	if !ok {
		t.Fatal("expected web_fetch in core network tools")
	}
	if property, ok := fetch.Parameters().Properties["url"]; !ok || property.Type != "string" {
		t.Fatalf("web_fetch must expose a string url property, got %#v", fetch.Parameters().Properties["url"])
	}
	if safety := fetch.Safety(); safety.Permission != PermissionPrompt || !safety.AdvertiseInAuto {
		t.Fatalf("web_fetch safety = %#v, want prompt and advertised in auto", safety)
	}

	// web_search discovers URLs; its key parameter is "query".
	search, ok := byName["web_search"]
	if !ok {
		t.Fatal("expected web_search in core network tools")
	}
	if property, ok := search.Parameters().Properties["query"]; !ok || property.Type != "string" {
		t.Fatalf("web_search must expose a string query property, got %#v", search.Parameters().Properties["query"])
	}
	if safety := search.Safety(); safety.Permission != PermissionAllow || safety.AdvertiseInAuto {
		t.Fatalf("web_search safety = %#v, want allow without prompt-advertise override", safety)
	}
}

func TestCoreNetworkToolsOmitWebSearchWhenUnconfigured(t *testing.T) {
	// No backend configured → don't offer web_search (it could only error, which
	// makes the model waste calls + prompts before falling back to an MCP search).
	t.Setenv("ZERO_WEBSEARCH_BASE_URL", "")
	names := map[string]bool{}
	for _, tool := range CoreNetworkTools() {
		names[tool.Name()] = true
	}
	if !names["web_fetch"] {
		t.Fatal("web_fetch should always be present")
	}
	if names["web_search"] {
		t.Fatal("web_search must be omitted when no search backend is configured")
	}
}

func TestCoreToolsIncludeWebFetchButReadOnlyToolsDoNot(t *testing.T) {
	readOnly := CoreReadOnlyTools(t.TempDir())
	for _, tool := range readOnly {
		if tool.Name() == "web_fetch" {
			t.Fatal("web_fetch should not be exposed by read-only core tools")
		}
	}

	found := false
	for _, tool := range CoreTools(t.TempDir()) {
		if tool.Name() == "web_fetch" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected CoreTools to include web_fetch")
	}
}

func TestRegistryRunsToolsThroughSafePath(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewReadFileTool(t.TempDir()))

	result := registry.Run(context.Background(), "read_file", map[string]any{
		"path": "missing.txt",
	})

	if result.Status != StatusError {
		t.Fatalf("expected read error status, got %s", result.Status)
	}
	if result.Output == "" {
		t.Fatalf("expected an error output")
	}
}

func TestRegistryRequiresPermissionForWebFetch(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewWebFetchTool())

	result := registry.Run(context.Background(), "web_fetch", map[string]any{
		"url": "https://example.com",
	})

	if result.Status != StatusError {
		t.Fatalf("expected permission error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "Permission required for web_fetch") {
		t.Fatalf("unexpected permission output: %q", result.Output)
	}
}

func TestRegistryRejectsLocalWebFetchBeforePermission(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewWebFetchTool())

	result := registry.Run(context.Background(), "web_fetch", map[string]any{
		"url": "http://localhost:8000/index.html",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if strings.Contains(result.Output, "Permission required") {
		t.Fatalf("local web_fetch must not request permission first: %q", result.Output)
	}
	if !strings.Contains(result.Output, "bash with curl") {
		t.Fatalf("expected curl guidance, got %q", result.Output)
	}
}

func TestRegistryReportsUnknownTools(t *testing.T) {
	result := NewRegistry().Run(context.Background(), "missing", map[string]any{})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if result.Output != `Error: Unknown tool "missing".` {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestRegistryAppliesSandboxBeforeToolExecution(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")
	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
	})

	result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":      outside,
		"content":   "escape",
		"overwrite": true,
	}, RunOptions{
		PermissionGranted: true,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionUnsafe),
		Autonomy:          "high",
	})

	if result.Status != StatusError {
		t.Fatalf("expected sandbox block status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "Sandbox block") || !strings.Contains(result.Output, "outside_workspace") {
		t.Fatalf("unexpected sandbox block output: %q", result.Output)
	}
}

// Regression: write_file/edit_file accept path aliases (file_path, filename); the
// sandbox must inspect those keys too, otherwise a model routes an out-of-workspace
// write around the gate by choosing an alias the sandbox doesn't see.
func TestRegistrySandboxGatesPathAliasKeys(t *testing.T) {
	for _, key := range []string{"file_path", "filename", "filepath"} {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "escape.txt")
		registry := NewRegistry()
		registry.Register(NewWriteFileTool(root))
		engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: root, Policy: sandbox.DefaultPolicy()})

		result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
			key:         outside,
			"content":   "escape",
			"overwrite": true,
		}, RunOptions{
			PermissionGranted: true,
			Sandbox:           engine,
			PermissionMode:    string(sandbox.PermissionUnsafe),
			Autonomy:          "high",
		})

		if result.Status != StatusError || !strings.Contains(result.Output, "outside_workspace") {
			t.Fatalf("alias %q bypassed the sandbox gate: %#v", key, result)
		}
		if _, err := os.Stat(outside); !os.IsNotExist(err) {
			t.Fatalf("alias %q: file written outside workspace", key)
		}
	}
}

func TestRegistryAllowsPromptToolWithPersistentSandboxGrant(t *testing.T) {
	root := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json"),
	})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	if _, err := store.Grant(sandbox.GrantInput{
		ToolName: "write_file",
		Decision: sandbox.GrantAllow,
		Reason:   "workspace writes",
	}); err != nil {
		t.Fatalf("Grant returned error: %v", err)
	}

	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Store:         store,
	})

	result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":      "granted.txt",
		"content":   "granted",
		"overwrite": true,
	}, RunOptions{
		PermissionGranted: false,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
		Autonomy:          "medium",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected persistent sandbox grant to authorize write_file, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "granted.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(content) != "granted" {
		t.Fatalf("written content = %q, want granted", string(content))
	}
}

type secretTool struct {
	out, display string
	meta         map[string]string
}

func (t secretTool) Name() string        { return "secret_tool" }
func (t secretTool) Description() string { return "emits text" }
func (t secretTool) Parameters() Schema  { return Schema{Type: "object", AdditionalProperties: false} }
func (t secretTool) Safety() Safety {
	return Safety{SideEffect: SideEffectRead, Permission: PermissionAllow}
}
func (t secretTool) Run(context.Context, map[string]any) Result {
	return Result{Status: StatusOK, Output: t.out, Display: Display{Summary: t.display}, Meta: t.meta}
}

// denyTool is permission-denied so RunWithOptions returns early via the denial
// path (before any tool execution); its Reason can carry secret-shaped text.
type denyTool struct{ reason string }

func (t denyTool) Name() string        { return "deny_tool" }
func (t denyTool) Description() string { return "always denied" }
func (t denyTool) Parameters() Schema  { return Schema{Type: "object", AdditionalProperties: false} }
func (t denyTool) Safety() Safety {
	return Safety{SideEffect: SideEffectShell, Permission: PermissionDeny, Reason: t.reason}
}
func (t denyTool) Run(context.Context, map[string]any) Result { return Result{Status: StatusOK} }

// Regression (Vasanthdev2004): the EARLY denial/permission/unknown-tool returns
// must be scrubbed too, not just the tool-execution paths.
func TestRunWithOptionsScrubsSecretsOnDenialPaths(t *testing.T) {
	secret := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	reg := NewRegistry()
	reg.Register(denyTool{reason: "blocked path=/tmp/" + secret})

	res := reg.RunWithOptions(context.Background(), "deny_tool", map[string]any{}, RunOptions{PermissionGranted: true})
	if res.Status != StatusError {
		t.Fatalf("expected permission-denied error, got %s: %s", res.Status, res.Output)
	}
	if strings.Contains(res.Output, secret) {
		t.Fatalf("denial path must scrub secrets, leaked: %q", res.Output)
	}
	if !res.Redacted {
		t.Error("expected Redacted=true on a scrubbed denial")
	}
}

// Regression: secrets must be scrubbed at the registry boundary so EVERY caller
// (agent loop AND MCP server) gets redacted output — not just the agent path.
func TestRunWithOptionsScrubsSecretsForAllCallers(t *testing.T) {
	secret := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	reg := NewRegistry()
	reg.Register(secretTool{
		out:     "token=" + secret,
		display: "ran token=" + secret,
		meta:    map[string]string{"pattern": "find " + secret},
	})

	res := reg.RunWithOptions(context.Background(), "secret_tool", map[string]any{}, RunOptions{PermissionGranted: true})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	if strings.Contains(res.Output, secret) {
		t.Fatalf("registry must scrub secrets, leaked: %q", res.Output)
	}
	// Display.Summary and Meta values must be scrubbed too — both are forwarded
	// into the transcript, so a caller preferring either must not bypass redaction.
	if strings.Contains(res.Display.Summary, secret) {
		t.Fatalf("registry must scrub Display.Summary, leaked: %q", res.Display.Summary)
	}
	if strings.Contains(res.Meta["pattern"], secret) {
		t.Fatalf("registry must scrub Meta values, leaked: %q", res.Meta["pattern"])
	}
	if !res.Redacted {
		t.Error("expected Redacted=true")
	}
}

func TestRunWithOptionsLeavesCleanOutputUnchanged(t *testing.T) {
	reg := NewRegistry()
	reg.Register(secretTool{out: "nothing secret here"})
	res := reg.RunWithOptions(context.Background(), "secret_tool", map[string]any{}, RunOptions{PermissionGranted: true})
	if res.Redacted || res.Output != "nothing secret here" {
		t.Fatalf("clean output altered: redacted=%v output=%q", res.Redacted, res.Output)
	}
}

// fakeDeferredTool implements the optional Deferred() interface and reports
// itself as deferred-eligible; fakePlainTool does not implement it.
type fakeDeferredTool struct {
	baseTool
	deferred bool
}

func (tool fakeDeferredTool) Run(context.Context, map[string]any) Result {
	return okResult("ok")
}

func (tool fakeDeferredTool) Deferred() bool { return tool.deferred }

type fakePlainTool struct {
	baseTool
}

func (tool fakePlainTool) Run(context.Context, map[string]any) Result {
	return okResult("ok")
}

func TestIsDeferredReportsOptionalInterface(t *testing.T) {
	eligible := fakeDeferredTool{
		baseTool: baseTool{name: "eligible", description: "deferred-eligible tool"},
		deferred: true,
	}
	if !IsDeferred(eligible) {
		t.Fatal("IsDeferred(eligible) = false, want true for a tool whose Deferred() returns true")
	}

	notEligible := fakeDeferredTool{
		baseTool: baseTool{name: "not_eligible", description: "implements interface but opts out"},
		deferred: false,
	}
	if IsDeferred(notEligible) {
		t.Fatal("IsDeferred(notEligible) = true, want false when Deferred() returns false")
	}

	plain := fakePlainTool{baseTool: baseTool{name: "plain", description: "no deferred interface"}}
	if IsDeferred(plain) {
		t.Fatal("IsDeferred(plain) = true, want false for a tool that does not implement deferredTool")
	}
}

// fakeUndeferredEligibleTool is exposed eagerly (Deferred()==false) yet still
// counts toward the threshold (DeferralEligible()==true) — like a swarm
// coordination tool while a swarm is active.
type fakeUndeferredEligibleTool struct {
	baseTool
}

func (tool fakeUndeferredEligibleTool) Run(context.Context, map[string]any) Result {
	return okResult("ok")
}

func (tool fakeUndeferredEligibleTool) Deferred() bool         { return false }
func (tool fakeUndeferredEligibleTool) DeferralEligible() bool { return true }

func TestIsDeferralEligibleDecouplesFromDeferred(t *testing.T) {
	// A currently-deferred tool is eligible via the IsDeferred fallback.
	deferred := fakeDeferredTool{baseTool: baseTool{name: "deferred"}, deferred: true}
	if !IsDeferralEligible(deferred) {
		t.Fatal("IsDeferralEligible(deferred) = false, want true (deferred tools always count)")
	}

	// Exposed eagerly but opted into DeferralEligible: still counts toward the gate.
	undeferred := fakeUndeferredEligibleTool{baseTool: baseTool{name: "undeferred_eligible"}}
	if IsDeferred(undeferred) {
		t.Fatal("IsDeferred(undeferred) = true, want false")
	}
	if !IsDeferralEligible(undeferred) {
		t.Fatal("IsDeferralEligible(undeferred) = false, want true (opts in via DeferralEligible)")
	}

	// A plain eager tool counts toward neither.
	plain := fakePlainTool{baseTool: baseTool{name: "plain"}}
	if IsDeferralEligible(plain) {
		t.Fatal("IsDeferralEligible(plain) = true, want false")
	}

	// Deferred()==false and no DeferralEligible interface: not eligible.
	optedOut := fakeDeferredTool{baseTool: baseTool{name: "opted_out"}, deferred: false}
	if IsDeferralEligible(optedOut) {
		t.Fatal("IsDeferralEligible(optedOut) = true, want false")
	}
}
