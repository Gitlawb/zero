package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestRunExecHelpDocumentsProtocolFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"exec", "--help"}, &stdout, &stderr)

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d", exitSuccess, exitCode)
	}
	for _, want := range []string{
		"--auto",
		"--enabled-tools",
		"--disabled-tools",
		"--list-tools",
		"--input-format text|stream-json",
		"--output-format text|json|stream-json",
		"--resume",
		"--fork",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected exec help to contain %q, got %q", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunExecListsFilteredToolsWithoutPromptOrProvider(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--list-tools", "--enabled-tools", "read_file,grep"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, errors.New("provider should not be resolved for --list-tools")
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	for _, want := range []string{"Tools visible to model", "read_file", "grep"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected tool list to contain %q, got %q", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "bash") {
		t.Fatalf("expected filtered tool list to hide bash, got %q", stdout.String())
	}
}

func TestRunExecListsToolsAsStreamJSONWhenRequested(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--list-tools", "--output-format", "stream-json", "--enabled-tools", "read_file"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, errors.New("provider should not be resolved for --list-tools")
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	events := decodeJSONLines(t, stdout.String())
	if len(events) != 3 {
		t.Fatalf("expected run_start, final, run_end events, got %#v", events)
	}
	if events[0]["type"] != "run_start" || events[1]["type"] != "final" || events[2]["type"] != "run_end" {
		t.Fatalf("unexpected stream-json tool list events: %#v", events)
	}
	text, _ := events[1]["text"].(string)
	if !strings.Contains(text, "Tools visible to model") || !strings.Contains(text, "read_file") {
		t.Fatalf("expected final event to contain tool list, got %#v", events[1])
	}
	for _, name := range []string{"bash", "grep", "write_file"} {
		if strings.Contains(text, name) {
			t.Fatalf("unexpected non-enabled tool %q leaked into stream-json output: %#v", name, events[1])
		}
	}
}

func TestRunExecListsMCPToolsWithoutProviderResolution(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	providerResolved := false
	closed := false

	exitCode := runWithDeps([]string{"exec", "--list-tools", "--enabled-tools", "mcp_docs_lookup"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			providerResolved = true
			return config.ResolvedConfig{}, errors.New("provider should not be resolved for --list-tools")
		},
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			return config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"docs": {Type: "stdio", Command: "docs-mcp"},
			}}, nil
		},
		registerMCPTools: func(ctx context.Context, registry *tools.Registry, cfg config.MCPConfig, options mcp.RegisterOptions) (mcpToolRuntime, error) {
			registry.Register(cliFakeMCPRegistryTool{})
			return closeFunc(func() error {
				closed = true
				return nil
			}), nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if providerResolved {
		t.Fatal("provider config should not be resolved for --list-tools")
	}
	if !closed {
		t.Fatal("MCP runtime was not closed after --list-tools")
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "mcp_docs_lookup") || !strings.Contains(stdout.String(), "Lookup documentation") {
		t.Fatalf("expected MCP tool in list output, got %q", stdout.String())
	}
}

func TestRunExecLogsMCPRuntimeCloseError(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--list-tools", "--enabled-tools", "mcp_docs_lookup"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			return config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"docs": {Type: "stdio", Command: "docs-mcp"},
			}}, nil
		},
		newMCPStore: func() (*mcp.PermissionStore, error) {
			return nil, nil
		},
		registerMCPTools: func(ctx context.Context, registry *tools.Registry, cfg config.MCPConfig, options mcp.RegisterOptions) (mcpToolRuntime, error) {
			registry.Register(cliFakeMCPRegistryTool{})
			return closeFunc(func() error {
				return errors.New("close failed")
			}), nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mcp_close_error: close failed") {
		t.Fatalf("stderr = %q, want MCP close error", stderr.String())
	}
}

func TestRunExecRejectsInvalidProtocolOptionsBeforeRuntime(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "auto", args: []string{"exec", "--auto", "chaos", "hello"}, want: "Invalid autonomy level"},
		{name: "enabled", args: []string{"exec", "--enabled-tools", "missing_tool", "hello"}, want: "Unknown tool"},
		{name: "overlap", args: []string{"exec", "--enabled-tools", "read_file", "--disabled-tools", "read_file", "hello"}, want: "both enabled and disabled"},
		{name: "input", args: []string{"exec", "--input-format", "yaml", "hello"}, want: "Invalid input format"},
		{name: "resume-fork", args: []string{"exec", "--resume", "abc", "--fork", "def", "hello"}, want: "Use either --resume or --fork, not both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run(tc.args, &stdout, &stderr)

			if exitCode != exitUsage {
				t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout before runtime, got %q", stdout.String())
			}
			if got := stderr.String(); !strings.Contains(got, tc.want) {
				t.Fatalf("expected stderr to contain %q, got %q", tc.want, got)
			}
		})
	}
}

func TestRunExecRejectsFlagShapedMissingValues(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"exec", "--model", "--output-format", "json", "hello"}, &stdout, &stderr)

	if exitCode != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "--model requires a value") {
		t.Fatalf("expected missing model value error, got %q", got)
	}
}

func TestRunExecStreamJSONUsageErrorsStayInProtocol(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--input-format", "stream-json", "--output-format", "stream-json"}, &stdout, &stderr, appDeps{
		stdin: strings.NewReader("{bad\n"),
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, errors.New("provider should not be resolved for invalid stream-json input")
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	events := decodeJSONLines(t, stdout.String())
	if len(events) != 2 || events[0]["type"] != "error" || events[1]["type"] != "run_end" {
		t.Fatalf("expected stream-json error and run_end, got %#v", events)
	}
	if events[0]["code"] != "usage_error" || !strings.Contains(events[0]["message"].(string), "Invalid stream-json input") {
		t.Fatalf("expected usage error event, got %#v", events[0])
	}
	if events[1]["exitCode"] != float64(exitUsage) {
		t.Fatalf("expected usage exit code, got %#v", events[1])
	}
}

func TestRunExecStreamJSONOutputsRunEndAndRecordsSession(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--output-format", "stream-json", "persist this"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	events := decodeJSONLines(t, stdout.String())
	types := jsonEventTypes(events)
	for _, want := range []string{"run_start", "text", "final", "run_end"} {
		if !slices.Contains(types, want) {
			t.Fatalf("expected event %q in %v; output %q", want, types, stdout.String())
		}
	}
	runStart := events[0]
	sessionID, ok := runStart["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("expected run_start sessionId, got %#v", runStart)
	}
	if runStart["provider"] != "openai-compatible" || runStart["apiModel"] != "echo-model" {
		t.Fatalf("expected resolved runtime metadata in run_start, got %#v", runStart)
	}
	if got := events[len(events)-1]["type"]; got != "run_end" {
		t.Fatalf("expected last event run_end, got %#v", events[len(events)-1])
	}

	store := sessions.NewStore(sessions.StoreOptions{
		RootDir: filepath.Join(dataHome, "zero", "sessions"),
	})
	recorded, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(recorded) != 2 || recorded[0].Type != sessions.EventMessage || recorded[1].Type != sessions.EventMessage {
		t.Fatalf("recorded events = %#v", recorded)
	}
}

func TestRunExecStreamJSONRunStartUsesResolvedAPIModel(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--output-format", "stream-json", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{
				ActiveProvider: "work",
				Provider: config.ProviderProfile{
					Name:  "work",
					Model: "sonnet-4.5",
				},
				MaxTurns: 2,
			}, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	events := decodeJSONLines(t, stdout.String())
	runStart := events[0]
	if runStart["provider"] != "anthropic" {
		t.Fatalf("expected resolved provider kind anthropic, got %#v", runStart)
	}
	if runStart["model"] != "sonnet-4.5" {
		t.Fatalf("expected logical model alias, got %#v", runStart)
	}
	if runStart["apiModel"] != "claude-sonnet-4-5-20250929" {
		t.Fatalf("expected resolved API model, got %#v", runStart)
	}
}

func TestExecEventWriterTruncatesStreamJSONToolResults(t *testing.T) {
	var stdout bytes.Buffer
	writer := execEventWriter{
		stdout:       &stdout,
		format:       execOutputStreamJSON,
		runID:        "run_test",
		streamedText: &strings.Builder{},
	}
	writer.toolResult(agent.ToolResult{
		ToolCallID: "call_1",
		Name:       "read_file",
		Status:     tools.StatusOK,
		Output:     strings.Repeat("x", streamJSONToolResultOutputLimit+100),
		Meta: map[string]string{
			"sandbox_backend": "bubblewrap",
			"sandbox_wrapped": "true",
		},
	})
	if writer.err != nil {
		t.Fatalf("toolResult returned writer error: %v", writer.err)
	}

	events := decodeJSONLines(t, stdout.String())
	if len(events) != 1 {
		t.Fatalf("expected one tool_result event, got %#v", events)
	}
	if events[0]["truncated"] != true {
		t.Fatalf("expected truncated=true, got %#v", events[0])
	}
	if events[0]["name"] != "read_file" {
		t.Fatalf("expected tool_result name to be read_file, got %#v", events[0])
	}
	meta := events[0]["meta"].(map[string]any)
	if meta["sandbox_backend"] != "bubblewrap" || meta["sandbox_wrapped"] != "true" {
		t.Fatalf("expected sandbox metadata, got %#v", meta)
	}
	output := events[0]["output"].(string)
	if len(output) >= streamJSONToolResultOutputLimit+100 || !strings.Contains(output, "[truncated]") {
		t.Fatalf("expected shortened output with marker, got len=%d", len(output))
	}
}

func TestRunExecStreamJSONProviderErrorEmitsErrorAndRunEnd(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--output-format", "stream-json", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, errors.New("provider failed")
		},
	})

	if exitCode != exitProvider {
		t.Fatalf("expected provider exit %d, got %d", exitProvider, exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	events := decodeJSONLines(t, stdout.String())
	if len(events) != 2 {
		t.Fatalf("expected error and run_end events, got %#v", events)
	}
	if events[0]["type"] != "error" || events[0]["code"] != "provider_error" {
		t.Fatalf("expected provider error event, got %#v", events[0])
	}
	if events[1]["type"] != "run_end" || events[1]["exitCode"] != float64(exitProvider) {
		t.Fatalf("expected run_end provider exit, got %#v", events[1])
	}
	if events[0]["runId"] != events[1]["runId"] {
		t.Fatalf("expected matching runId, got %#v", events)
	}
}

func TestRunExecReadsStreamJSONPromptFromStdin(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--input-format", "stream-json", "--output-format", "stream-json"}, &stdout, &stderr, appDeps{
		stdin: strings.NewReader(`{"schemaVersion":1,"type":"prompt","content":"from stdin"}` + "\n"),
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	events := decodeJSONLines(t, stdout.String())
	final := events[len(events)-2]
	if final["type"] != "final" || final["text"] != "from stdin" {
		t.Fatalf("expected final event from stdin, got %#v", final)
	}
}

type cliFakeMCPRegistryTool struct{}

func (cliFakeMCPRegistryTool) Name() string {
	return "mcp_docs_lookup"
}

func (cliFakeMCPRegistryTool) Description() string {
	return "Lookup documentation"
}

func (cliFakeMCPRegistryTool) Parameters() tools.Schema {
	return tools.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Properties: map[string]tools.PropertySchema{
			"query": {Type: "string"},
		},
	}
}

func (cliFakeMCPRegistryTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectNetwork,
		Permission: tools.PermissionAllow,
		Reason:     "MCP test tool",
	}
}

func (cliFakeMCPRegistryTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK, Output: "ok"}
}

type closeFunc func() error

func (fn closeFunc) Close() error {
	return fn()
}
