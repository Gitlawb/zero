package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sessions"
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
