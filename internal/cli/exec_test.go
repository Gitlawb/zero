package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestRunExecHelpDocumentsM1Flags(t *testing.T) {
	for _, args := range [][]string{
		{"exec", "--help"},
		{"exec", "--help", "--model", "m1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run(args, &stdout, &stderr)

			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			for _, want := range []string{
				"-f, --file",
				"-m, --model",
				"--max-turns",
				"--profile <profile>",
				"-r, --reasoning-effort <effort>",
				"-C, --cwd",
				"-o, --output-format text|json",
				"--prompt",
				"--calling-session-id",
				"--calling-tool-use-id",
				"--tag <tag>",
				"--depth <number>",
				"--session-title",
				"--init-session-id",
				"--skip-permissions-unsafe",
			} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("expected exec help to contain %q, got %q", want, stdout.String())
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
		})
	}
}

func TestRunExecRejectsInvalidMaxTurnsBeforeRuntime(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{value: "nope", want: "invalid --max-turns"},
		{value: "-1", want: "invalid --max-turns"},
		{value: "0", want: "invalid --max-turns"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run([]string{"exec", "--max-turns", tc.value, "hello"}, &stdout, &stderr)

			if exitCode != exitUsage {
				t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout before runtime, got %q", stdout.String())
			}
			if got := stderr.String(); !strings.Contains(got, tc.want) {
				t.Fatalf("expected max-turns validation error containing %q, got %q", tc.want, got)
			}
		})
	}

	t.Run("equals-empty", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := Run([]string{"exec", "--max-turns=", "hello"}, &stdout, &stderr)

		if exitCode != exitUsage {
			t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected empty stdout before runtime, got %q", stdout.String())
		}
		if got := stderr.String(); !strings.Contains(got, "--max-turns requires a value") {
			t.Fatalf("expected empty max-turns validation error, got %q", got)
		}
	})
}

func TestRunExecMaxTurnsReachesConfigOverrides(t *testing.T) {
	cwd := t.TempDir()
	var gotMaxTurns int

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--max-turns", "7", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			gotMaxTurns = overrides.MaxTurns
			return config.ResolvedConfig{}, errors.New("stop before provider")
		},
	})

	if exitCode != exitProvider {
		t.Fatalf("expected provider exit %d, got %d", exitProvider, exitCode)
	}
	if gotMaxTurns != 7 {
		t.Fatalf("overrides.MaxTurns = %d, want 7", gotMaxTurns)
	}
}

func TestParseExecSpecialistMetadataFlags(t *testing.T) {
	options, help, err := parseExecArgs([]string{
		"--calling-session-id", "parent_session",
		"--calling-tool-use-id=toolu_123",
		"--tag", "specialist",
		"--depth=2",
		"--session-title", "Explorer child",
		"--init-session-id", "child_session",
		"--output-format", "debug",
		"inspect the parser",
	})
	if err != nil {
		t.Fatalf("parseExecArgs returned error: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	if options.callingSessionID != "parent_session" ||
		options.callingToolUseID != "toolu_123" ||
		options.tag != "specialist" ||
		options.depth != 2 ||
		options.sessionTitle != "Explorer child" ||
		options.initSessionID != "child_session" {
		t.Fatalf("metadata flags did not parse correctly: %#v", options)
	}
	if options.outputFormat != execOutputStreamJSON {
		t.Fatalf("outputFormat = %q, want stream-json debug alias", options.outputFormat)
	}
	if strings.Join(options.promptParts, " ") != "inspect the parser" {
		t.Fatalf("promptParts = %#v", options.promptParts)
	}
}

func TestParseExecSpecialistMetadataRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "negative depth", args: []string{"--depth=-1", "hello"}, want: "invalid --depth"},
		{name: "non numeric depth", args: []string{"--depth", "many", "hello"}, want: "invalid --depth"},
		{name: "empty tag", args: []string{"--tag=", "hello"}, want: "--tag requires a value"},
		{name: "bad init session", args: []string{"--init-session-id", "../escape", "hello"}, want: "invalid --init-session-id"},
		{name: "init with resume", args: []string{"--init-session-id", "child", "--resume", "parent", "hello"}, want: "Use --init-session-id only"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseExecArgs(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestRunExecUsesInitSessionIDAndSessionTitle(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	cwd := t.TempDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"exec",
		"--init-session-id", "specialist_child",
		"--session-title", "Explorer child",
		"hello",
	}, &stdout, &stderr, appDeps{
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
		t.Fatalf("exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}

	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(dataHome, "zero", "sessions")})
	session, err := store.Get("specialist_child")
	if err != nil {
		t.Fatalf("Get session returned error: %v", err)
	}
	if session == nil {
		t.Fatal("expected initialized session specialist_child")
	}
	if session.Title != "Explorer child" {
		t.Fatalf("session title = %q, want Explorer child", session.Title)
	}
	if session.Cwd != cwd {
		t.Fatalf("session cwd = %q, want %q", session.Cwd, cwd)
	}
}

func TestRunExecAcceptsLegacyModelProfileFlags(t *testing.T) {
	exitCode, stdout, stderr := runExecWithEcho(t, []string{
		"exec",
		"--profile",
		"fast",
		"--reasoning-effort",
		"low",
		"hello",
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr)
	}
	if !strings.Contains(stdout, "hello") {
		t.Fatalf("expected prompt output, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunExecJSONRunStartWriteFailureSkipsAgent(t *testing.T) {
	cwd := t.TempDir()
	called := false

	exitCode := runWithDeps([]string{"exec", "-o", "json", "hello"}, failingWriter{}, io.Discard, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return recordingExecProvider{called: &called}, nil
		},
	})

	if exitCode != exitCrash {
		t.Fatalf("expected exit code %d, got %d", exitCrash, exitCode)
	}
	if called {
		t.Fatal("expected agent provider not to run after run_start write failure")
	}
}

func TestRunExecUnsafeWarningWriteFailureSkipsAgent(t *testing.T) {
	cwd := t.TempDir()
	called := false

	exitCode := runWithDeps([]string{"exec", "--skip-permissions-unsafe", "hello"}, io.Discard, failingWriter{}, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return recordingExecProvider{called: &called}, nil
		},
	})

	if exitCode != exitCrash {
		t.Fatalf("expected exit code %d, got %d", exitCrash, exitCode)
	}
	if called {
		t.Fatal("expected agent provider not to run after warning write failure")
	}
}

func TestRunExecJSONProviderErrorWriteFailureReturnsCrash(t *testing.T) {
	cwd := t.TempDir()

	exitCode := runWithDeps([]string{"exec", "-o", "json", "hello"}, failingWriter{}, io.Discard, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, errors.New("provider config failed")
		},
	})

	if exitCode != exitCrash {
		t.Fatalf("expected exit code %d, got %d", exitCrash, exitCode)
	}
}

func execResolvedConfig() config.ResolvedConfig {
	return config.ResolvedConfig{
		ActiveProvider: "echo",
		Provider: config.ProviderProfile{
			Name:         "echo",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "http://127.0.0.1/v1",
			Model:        "echo-model",
		},
	}
}

type recordingExecProvider struct {
	called *bool
}

func (provider recordingExecProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	*provider.called = true
	return nil, errors.New("provider should not run")
}

func TestRunPromptFlagRoutesToExecRunner(t *testing.T) {
	execExitCode, execStdout, execStderr := runExecWithEcho(t, []string{"exec", "hello zero"})

	for _, args := range [][]string{
		{"-p", "hello zero"},
		{"--prompt", "hello zero"},
	} {
		t.Run(args[0], func(t *testing.T) {
			exitCode, stdout, stderr := runExecWithEcho(t, args)

			if exitCode != execExitCode {
				t.Fatalf("expected exit code %d, got %d", execExitCode, exitCode)
			}
			if stdout != execStdout {
				t.Fatalf("expected stdout %q, got %q", execStdout, stdout)
			}
			if stderr != execStderr {
				t.Fatalf("expected stderr %q, got %q", execStderr, stderr)
			}
		})
	}
}

func TestRunExecAssemblesInlineAndFilePromptRelativeToCwd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prompt.txt"), []byte("file prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	exitCode, stdout, stderr := runExecWithEcho(t, []string{"exec", "--cwd", root, "--file", "prompt.txt", "inline prompt"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr)
	}
	if !strings.Contains(stdout, "inline prompt\n\nfile prompt") {
		t.Fatalf("expected inline and file prompt joined by blank line, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunExecAcceptsFileOnlyPrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prompt.txt"), []byte("file only prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	exitCode, stdout, stderr := runExecWithEcho(t, []string{"exec", "-C", root, "-f", "prompt.txt"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr)
	}
	if !strings.Contains(stdout, "file only prompt") {
		t.Fatalf("expected file prompt output, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunExecRejectsInvalidCwdBeforeRuntime(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-directory.txt")
	if err := os.WriteFile(filePath, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		cwd  string
	}{
		{name: "missing", cwd: filepath.Join(root, "missing")},
		{name: "file", cwd: filePath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run([]string{"exec", "--cwd", tc.cwd, "hello"}, &stdout, &stderr)

			if exitCode != 2 {
				t.Fatalf("expected exit code 2, got %d", exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout before runtime, got %q", stdout.String())
			}
			if got := stderr.String(); !strings.Contains(got, "cwd must be an existing directory") {
				t.Fatalf("expected cwd validation error, got %q", got)
			}
			if strings.Contains(stdout.String()+stderr.String(), "Go agent runtime ready") {
				t.Fatalf("expected validation before runtime, got stdout %q stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunExecRejectsInvalidOutputFormatBeforeRuntime(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"exec", "-o", "yaml", "hello"}, &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout before runtime, got %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `invalid output format "yaml"`) || !strings.Contains(got, "debug") {
		t.Fatalf("expected output format validation error, got %q", got)
	}
	if strings.Contains(stdout.String()+stderr.String(), "Go agent runtime ready") {
		t.Fatalf("expected validation before runtime, got stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestRunExecUnsafeTextModeWarns(t *testing.T) {
	exitCode, stdout, stderr := runExecWithEcho(t, []string{"exec", "--skip-permissions-unsafe", "hello"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr)
	}
	if !strings.Contains(stdout, "hello") {
		t.Fatalf("expected prompt in stdout, got %q", stdout)
	}
	if got := stderr; !strings.Contains(got, "WARNING") || !strings.Contains(got, "--skip-permissions-unsafe") {
		t.Fatalf("expected unsafe warning, got %q", got)
	}
}

func TestRunExecJSONOutputsNDJSONEvents(t *testing.T) {
	root := t.TempDir()

	exitCode, stdout, stderr := runExecWithEcho(t, []string{"exec", "--cwd", root, "-m", "m1-test", "-o", "json", "hello json"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}

	events := decodeJSONLines(t, stdout)
	eventTypes := jsonEventTypes(events)
	for _, want := range []string{"run_start", "text", "final", "done"} {
		if !slices.Contains(eventTypes, want) {
			t.Fatalf("expected JSON event %q in %v; output %q", want, eventTypes, stdout)
		}
	}
	if got := events[0]["type"]; got != "run_start" {
		t.Fatalf("expected first event run_start, got %v", got)
	}
	if got := events[0]["model"]; got != "m1-test" {
		t.Fatalf("expected run_start model m1-test, got %v", got)
	}
	if got := events[0]["cwd"]; got != root {
		t.Fatalf("expected run_start cwd %q, got %v", root, got)
	}
	if got := events[0]["permission_mode"]; got != "auto" {
		t.Fatalf("expected default permission_mode auto, got %v", got)
	}
}

func TestRunExecJSONUnsafeOutputsWarningEvent(t *testing.T) {
	exitCode, stdout, stderr := runExecWithEcho(t, []string{"exec", "--skip-permissions-unsafe", "-o", "json", "hello"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}

	events := decodeJSONLines(t, stdout)
	eventTypes := jsonEventTypes(events)
	if !slices.Contains(eventTypes, "warning") {
		t.Fatalf("expected JSON warning event in %v; output %q", eventTypes, stdout)
	}
	if got := events[0]["permission_mode"]; got != "unsafe" {
		t.Fatalf("expected run_start permission_mode unsafe, got %v", got)
	}
}

func TestRunExecUsesProjectConfigAndOpenAICompatibleProvider(t *testing.T) {
	clearProviderEnv(t)
	root := t.TempDir()
	configDir := filepath.Join(root, ".zero")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var gotAuth string
	var gotMethod string
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"provider ok\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	writeConfig := `{
		"activeProvider": "local",
		"providers": [{
			"name": "local",
			"provider_kind": "openai-compatible",
			"base_url": "` + server.URL + `",
			"api_key": "sk-local",
			"model": "local-model"
		}]
	}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(writeConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run([]string{"exec", "--cwd", root, "hello provider"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "provider ok" {
		t.Fatalf("stdout = %q, want provider response", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if gotAuth != "Bearer sk-local" {
		t.Fatalf("Authorization = %q, want project config token", gotAuth)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Fatalf("path = %q, want suffix /chat/completions", gotPath)
	}
	if gotBody["model"] != "local-model" {
		t.Fatalf("provider model = %v, want local-model", gotBody["model"])
	}
	messages, ok := gotBody["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages = %#v, want non-empty []any", gotBody["messages"])
	}
	lastMessage, ok := messages[len(messages)-1].(map[string]any)
	if !ok {
		t.Fatalf("last message = %#v, want map[string]any", messages[len(messages)-1])
	}
	if lastMessage["content"] != "hello provider" {
		t.Fatalf("last provider message = %#v, want prompt", lastMessage)
	}
}

func runExecWithEcho(t *testing.T, args []string) (int, string, string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cwd := t.TempDir()
	exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			model := "echo-model"
			if overrides.Provider.Model != "" {
				model = overrides.Provider.Model
			}
			return config.ResolvedConfig{
				ActiveProvider: "echo",
				Provider: config.ProviderProfile{
					Name:         "echo",
					ProviderKind: config.ProviderKindOpenAICompatible,
					BaseURL:      "http://127.0.0.1/v1",
					Model:        model,
				},
				MaxTurns: 3,
			}, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})
	return exitCode, stdout.String(), stderr.String()
}

type echoExecProvider struct{}

func (echoExecProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	prompt := ""
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == zeroruntime.MessageRoleUser {
			prompt = request.Messages[index].Content
			break
		}
	}
	ch := make(chan zeroruntime.StreamEvent, 2)
	select {
	case <-ctx.Done():
		close(ch)
		return ch, ctx.Err()
	case ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: prompt}:
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func clearProviderEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"ZERO_PROVIDER_COMMAND",
		"ZERO_PROVIDER",
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"OPENAI_MODEL",
	} {
		t.Setenv(key, "")
	}
}

func decodeJSONLines(t *testing.T, output string) []map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("expected JSON lines, got %q", output)
	}

	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("expected JSON object line, got %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func jsonEventTypes(events []map[string]any) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		eventType, _ := event["type"].(string)
		types = append(types, eventType)
	}
	return types
}
