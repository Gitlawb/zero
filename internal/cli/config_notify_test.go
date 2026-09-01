package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

// `zero config notify` with no flag and a fresh config: the resolver applies
// the built-in defaults (mode=both, focusMode=unfocused) and the command
// reports them. This is the "just works" case a new user lands in. We use a
// real on-disk config (not the synthetic commandCenterDeps fixture, which
// returns an empty Notify field) because the resolver-default behavior is the
// whole point of this test.
func TestRunConfigNotifyPrintsResolverDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	// A valid openai profile so the resolver does not error with
	// ErrNoActiveProvider. The notify defaults are applied independently of
	// the provider resolution path.
	seed := `{
		"activeProvider": "openai",
		"providers": [{
			"name": "openai",
			"providerKind": "openai",
			"baseUrl": "https://api.openai.com/v1",
			"model": "gpt-4.1",
			"apiKeyEnv": "OPENAI_API_KEY"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	deps := commandCenterDeps(t)
	deps.userConfigPath = func() (string, error) { return configPath, nil }
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		return config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{"OPENAI_API_KEY": "sk-test"}})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"config", "notify"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if !strings.Contains(stdout.String(), "mode:      both") {
		t.Errorf("stdout should show the default mode, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "focusMode: unfocused") {
		t.Errorf("stdout should show the default focus, got: %s", stdout.String())
	}
}

// `zero config notify --json` emits a machine-readable payload so scripts can
// read the resolved preference without parsing prose. Same real-resolver
// fixture as the print-defaults test, since the JSON path reads the same
// `resolved.Notify` that the print path does.
func TestRunConfigNotifyPrintsJSON(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := `{
		"activeProvider": "openai",
		"providers": [{
			"name": "openai",
			"providerKind": "openai",
			"baseUrl": "https://api.openai.com/v1",
			"model": "gpt-4.1",
			"apiKeyEnv": "OPENAI_API_KEY"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	deps := commandCenterDeps(t)
	deps.userConfigPath = func() (string, error) { return configPath, nil }
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		return config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{"OPENAI_API_KEY": "sk-test"}})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"config", "notify", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["mode"] != "both" {
		t.Errorf("mode = %v, want both", payload["mode"])
	}
	if payload["focusMode"] != "unfocused" {
		t.Errorf("focusMode = %v, want unfocused", payload["focusMode"])
	}
}

// `zero config notify --mode off` writes the new value to disk and prints
// confirmation. The user can read it back by running the command again.
func TestRunConfigNotifyWritesModeChange(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := `{
		"activeProvider": "openai",
		"providers": [{
			"name": "openai",
			"providerKind": "openai",
			"baseUrl": "https://api.openai.com/v1",
			"model": "gpt-4.1",
			"apiKeyEnv": "OPENAI_API_KEY"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	deps := commandCenterDeps(t)
	deps.userConfigPath = func() (string, error) { return configPath, nil }
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		return config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{"OPENAI_API_KEY": "sk-test"}})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"config", "notify", "--mode", "off"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	cfg := readFileConfig(t, configPath)
	if cfg.Notify.Mode != "off" {
		t.Errorf("Notify.Mode = %q, want off", cfg.Notify.Mode)
	}
	if !strings.Contains(stdout.String(), "mode:      off") {
		t.Errorf("stdout should confirm the change, got: %s", stdout.String())
	}
}

// `--mode` and `--focus` together update both fields in one call.
func TestRunConfigNotifyWritesModeAndFocus(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := `{
		"activeProvider": "openai",
		"providers": [{
			"name": "openai",
			"providerKind": "openai",
			"baseUrl": "https://api.openai.com/v1",
			"model": "gpt-4.1",
			"apiKeyEnv": "OPENAI_API_KEY"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	deps := commandCenterDeps(t)
	deps.userConfigPath = func() (string, error) { return configPath, nil }
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		return config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{"OPENAI_API_KEY": "sk-test"}})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"config", "notify", "--mode", "both", "--focus", "unfocused"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	cfg := readFileConfig(t, configPath)
	if cfg.Notify.Mode != "both" || cfg.Notify.FocusMode != "unfocused" {
		t.Errorf("Notify = %+v, want mode=both focusMode=unfocused", cfg.Notify)
	}
}

// `--mode loud` is a usage error. The config must not be mutated.
func TestRunConfigNotifyRejectsInvalidMode(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := `{
		"activeProvider": "openai",
		"providers": [{
			"name": "openai",
			"providerKind": "openai",
			"baseUrl": "https://api.openai.com/v1",
			"model": "gpt-4.1",
			"apiKeyEnv": "OPENAI_API_KEY"
		}],
		"notify": {"mode": "off"}
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	deps := commandCenterDeps(t)
	deps.userConfigPath = func() (string, error) { return configPath, nil }
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		return config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{"OPENAI_API_KEY": "sk-test"}})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"config", "notify", "--mode", "loud"}, &stdout, &stderr, deps)
	if exitCode == exitSuccess {
		t.Fatalf("expected failure for invalid mode, got success; stdout=%s", stdout.String())
	}
	cfg := readFileConfig(t, configPath)
	if cfg.Notify.Mode != "off" {
		t.Errorf("Notify.Mode = %q, want off (unchanged after failed write)", cfg.Notify.Mode)
	}
}

// `--reset` blanks both fields so the resolver defaults apply on the next
// resolve. Useful for "go back to the recommended setup" after a custom value.
func TestRunConfigNotifyResetClearsStoredValues(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := `{
		"activeProvider": "openai",
		"providers": [{
			"name": "openai",
			"providerKind": "openai",
			"baseUrl": "https://api.openai.com/v1",
			"model": "gpt-4.1",
			"apiKeyEnv": "OPENAI_API_KEY"
		}],
		"notify": {"mode": "off", "focusMode": "always"}
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	deps := commandCenterDeps(t)
	deps.userConfigPath = func() (string, error) { return configPath, nil }
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		return config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{"OPENAI_API_KEY": "sk-test"}})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"config", "notify", "--reset"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	cfg := readFileConfig(t, configPath)
	if cfg.Notify.Mode != "" || cfg.Notify.FocusMode != "" {
		t.Errorf("Notify after reset = %+v, want empty (defaults apply)", cfg.Notify)
	}
}

// `zero config` (no subcommand) still works after the dispatch change.
func TestRunConfigSummaryStillWorks(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"config"}, &stdout, &stderr, commandCenterDeps(t))
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Config") {
		t.Errorf("stdout should show the config summary, got: %s", stdout.String())
	}
}
