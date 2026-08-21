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

func TestRunProvidersUseSetsActiveProvider(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{
		ActiveProvider: "work",
		Providers: []config.ProviderProfile{
			{Name: "work", ProviderKind: config.ProviderKindOpenAI, BaseURL: config.OpenAIBaseURL, Model: "gpt-4.1"},
			{Name: "fast", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", Model: "llama-3.3-70b-versatile"},
		},
	})

	exitCode := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	cfg := readFileConfig(t, configPath)
	if cfg.ActiveProvider != "fast" {
		t.Fatalf("ActiveProvider = %q, want fast", cfg.ActiveProvider)
	}
	output := stdout.String()
	for _, want := range []string{"Active provider set to fast", "zero providers check fast"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected providers use output to contain %q, got %q", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunProvidersRepairConfigRecoversLegacyUnnamedProvider(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		name := "text"
		if jsonOutput {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			configPath := filepath.Join(t.TempDir(), "zero", "config.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
				t.Fatal(err)
			}
			seed := []byte(`{"activeProvider":"legacy","providers":[{"name":"","provider_kind":"openai","model":"gpt-4o"}],"maxTurns":17}`)
			if err := os.WriteFile(configPath, seed, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{}}); err == nil {
				t.Fatal("legacy unnamed config unexpectedly resolved before repair")
			}
			args := []string{"providers", "repair-config"}
			if jsonOutput {
				args = append(args, "--json")
			}
			code := runWithDeps(args, &stdout, &stderr, providerSetupDeps(configPath))
			if code != exitSuccess {
				t.Fatalf("repair exit = %d, stderr=%q", code, stderr.String())
			}
			resolved, err := config.Resolve(config.ResolveOptions{UserConfigPath: configPath, Env: map[string]string{}})
			if err != nil {
				t.Fatalf("repaired config does not resolve: %v", err)
			}
			if resolved.ActiveProvider != "legacy" || resolved.Provider.Name != "legacy" || resolved.Provider.Model != "gpt-4o" || resolved.MaxTurns != 17 {
				t.Fatalf("resolved repaired config = %+v", resolved)
			}
			if jsonOutput {
				var payload map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil || payload["repairedProvider"] != "legacy" {
					t.Fatalf("repair JSON = %q, err=%v", stdout.String(), err)
				}
			} else if !strings.Contains(stdout.String(), "Named legacy provider legacy") {
				t.Fatalf("repair output = %q", stdout.String())
			}
		})
	}
}

func TestProviderRepairCommandsCanResolveIndependentLegacyNameProblems(t *testing.T) {
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{Providers: []config.ProviderProfile{
		{Name: ""}, {Name: "work"}, {Name: "WORK"},
	}})
	deps := providerSetupDeps(configPath)
	if code := runWithDeps([]string{"providers", "repair-config", "--name", "legacy"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("repair-config exit=%d stderr=%q", code, stderr.String())
	}
	if err := config.ValidatePersistedProviderNames(readFileConfig(t, configPath)); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("first repair should leave only the independent duplicate issue, got %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"providers", "remove", "WORK"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("remove exit=%d stderr=%q", code, stderr.String())
	}
	if err := config.ValidatePersistedProviderNames(readFileConfig(t, configPath)); err != nil {
		t.Fatalf("final config remains invalid: %v", err)
	}
}

func TestRunProvidersUseJSONIncludesActiveProviderAndConfigPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{
		ActiveProvider: "work",
		Providers: []config.ProviderProfile{
			{Name: "work", ProviderKind: config.ProviderKindOpenAI, BaseURL: config.OpenAIBaseURL, Model: "gpt-4.1"},
			{Name: "fast", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", Model: "llama-3.3-70b-versatile"},
		},
	})

	exitCode := runWithDeps([]string{"providers", "use", "fast", "--json"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var payload struct {
		ActiveProvider string `json:"activeProvider"`
		ConfigPath     string `json:"configPath"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("providers use JSON did not decode: %v\n%s", err, stdout.String())
	}
	if payload.ActiveProvider != "fast" || payload.ConfigPath != configPath {
		t.Fatalf("unexpected providers use JSON payload: %#v", payload)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func providersUseOverrideConfig(t *testing.T) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{
		ActiveProvider: "work",
		Providers: []config.ProviderProfile{
			{Name: "work", ProviderKind: config.ProviderKindOpenAI, BaseURL: config.OpenAIBaseURL, Model: "gpt-4.1"},
			{Name: "fast", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", Model: "llama-3.3-70b-versatile"},
		},
	})
	return configPath
}

// The write to config.json still succeeds, but when ZERO_PROVIDER names a
// different provider the saved selection is NOT effective, so the command must
// warn instead of reporting a silent success (issue #721).
func TestRunProvidersUseWarnsWhenEnvOverrides(t *testing.T) {
	var stdout, stderr bytes.Buffer
	configPath := providersUseOverrideConfig(t)
	deps := providerSetupDeps(configPath)
	deps.getenv = func(key string) string {
		if key == config.ActiveProviderEnv {
			return "work" // ZERO_PROVIDER=work overrides the switch to fast
		}
		return ""
	}

	if code := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	if cfg := readFileConfig(t, configPath); cfg.ActiveProvider != "fast" {
		t.Fatalf("ActiveProvider = %q, want fast (the write still lands)", cfg.ActiveProvider)
	}
	if !strings.Contains(stdout.String(), "Active provider set to fast") {
		t.Fatalf("stdout missing the success line: %q", stdout.String())
	}
	note := stderr.String()
	for _, want := range []string{config.ActiveProviderEnv, "overrides config.json", "work"} {
		if !strings.Contains(note, want) {
			t.Fatalf("override note missing %q, got %q", want, note)
		}
	}
}

func TestRunProvidersUseJSONFlagsEnvOverride(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := providerSetupDeps(providersUseOverrideConfig(t))
	deps.getenv = func(key string) string {
		if key == config.ActiveProviderEnv {
			return "work"
		}
		return ""
	}

	if code := runWithDeps([]string{"providers", "use", "fast", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	var payload struct {
		ActiveProvider    string `json:"activeProvider"`
		EffectiveProvider string `json:"effectiveProvider"`
		OverriddenByEnv   string `json:"overriddenByEnv"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("JSON did not decode: %v\n%s", err, stdout.String())
	}
	if payload.ActiveProvider != "fast" || payload.EffectiveProvider != "work" || payload.OverriddenByEnv != config.ActiveProviderEnv {
		t.Fatalf("JSON must flag the env override, got %#v", payload)
	}
}

// No override note when ZERO_PROVIDER is unset or already names the selection.
func TestRunProvidersUseNoWarnWithoutEnvOverride(t *testing.T) {
	cases := map[string]func(string) string{
		"env unset": func(string) string { return "" },
		"env matches fast": func(key string) string {
			if key == config.ActiveProviderEnv {
				return "fast"
			}
			return ""
		},
	}
	for name, getenv := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			deps := providerSetupDeps(providersUseOverrideConfig(t))
			deps.getenv = getenv
			if code := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, deps); code != exitSuccess {
				t.Fatalf("exit = %d: %s", code, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no override note, got %q", stderr.String())
			}
		})
	}
}

func TestRunProvidersUseSurfacesMalformedConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}

	exitCode := runWithDeps([]string{"providers", "use", "openai"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitCrash {
		t.Fatalf("exit code = %d, want %d", exitCode, exitCrash)
	}
	if !strings.Contains(stderr.String(), "invalid config JSON") {
		t.Fatalf("stderr = %q, want malformed-config error", stderr.String())
	}
}

func TestRunProvidersUseEnvDerivedJSONIncludesConfigPath(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{})

	exitCode := runWithDeps([]string{"providers", "use", "openai", "--json"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	var payload struct {
		ConfigPath string `json:"configPath"`
		Persisted  bool   `json:"persisted"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if payload.ConfigPath != configPath || payload.Persisted {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestRunProvidersRemoveEnvDerivedJSONKeepsSchema(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{})

	exitCode := runWithDeps([]string{"providers", "remove", "openai", "--json"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	var payload struct {
		Removed    string `json:"removed"`
		KeyRemoved bool   `json:"keyRemoved"`
		Persisted  bool   `json:"persisted"`
		ConfigPath string `json:"configPath"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if payload.Removed != "" || payload.KeyRemoved || payload.Persisted || payload.ConfigPath != configPath {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestRunProvidersRenameEnvDerivedExplainsNoSavedProfile(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{})

	exitCode := runWithDeps([]string{"providers", "rename", "openai", "renamed"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no saved profile to rename") {
		t.Fatalf("stdout = %q, want unpersisted explanation", stdout.String())
	}
}

func TestRunProvidersRenameEnvDerivedJSONKeepsSchema(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{})

	exitCode := runWithDeps([]string{"providers", "rename", "openai", "renamed", "--json"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	var payload struct {
		Renamed    *struct{} `json:"renamed"`
		ConfigPath string    `json:"configPath"`
		Persisted  bool      `json:"persisted"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if payload.Renamed != nil || payload.ConfigPath != configPath || payload.Persisted {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestRunProvidersUseRejectsUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing name", args: []string{"providers", "use"}, want: "provider name is required"},
		{name: "extra arg", args: []string{"providers", "use", "fast", "extra"}, want: `unexpected argument "extra"`},
		{name: "unknown flag", args: []string{"providers", "use", "fast", "--bogus"}, want: `unknown flag "--bogus"`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithDeps(tt.args, &stdout, &stderr, providerSetupDeps(filepath.Join(t.TempDir(), "config.json")))

			if exitCode != exitUsage {
				t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("expected stderr to contain %q, got %q", tt.want, stderr.String())
			}
		})
	}
}

func TestRunProvidersSetupPrintsCommandPlan(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")

	exitCode := runWithDeps([]string{
		"providers", "setup", "groq",
		"--name", "fast",
		"--model", "llama-3.1-70b",
		"--base-url", "https://gateway.example/v1",
		"--api-key-env", "FAST_API_KEY",
	}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"Set FAST_API_KEY to your API key",
		"zero providers add groq --name fast --model llama-3.1-70b --base-url https://gateway.example/v1 --api-key-env FAST_API_KEY",
		"zero providers check fast --connectivity",
		"zero providers use fast",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected setup output to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "sk-") {
		t.Fatalf("setup output leaked a secret-looking value: %q", output)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("providers setup should not write config, stat err = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunProvidersSetupJSONIncludesCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")

	exitCode := runWithDeps([]string{"providers", "setup", "groq", "--name", "fast", "--set-active", "--json"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var payload struct {
		CatalogID    string `json:"catalogID"`
		Name         string `json:"name"`
		AddCommand   string `json:"addCommand"`
		CheckCommand string `json:"checkCommand"`
		UseCommand   string `json:"useCommand"`
		EnvVar       string `json:"envVar"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("providers setup JSON did not decode: %v\n%s", err, stdout.String())
	}
	if payload.CatalogID != "groq" ||
		payload.Name != "fast" ||
		payload.AddCommand != "zero providers add groq --name fast --api-key-env GROQ_API_KEY --set-active" ||
		payload.CheckCommand != "zero providers check fast --connectivity" ||
		payload.UseCommand != "" ||
		payload.EnvVar != "GROQ_API_KEY" {
		t.Fatalf("unexpected setup JSON payload: %#v", payload)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("providers setup should not write config, stat err = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunProvidersSetupRejectsCatalogOnlyTransports(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")

	exitCode := runWithDeps([]string{"providers", "setup", "bedrock"}, &stdout, &stderr, providerSetupDeps(configPath))

	if exitCode != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "native adapter") {
		t.Fatalf("expected native adapter warning, got %q", stderr.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("providers setup should not write config, stat err = %v", err)
	}
}

func TestRunProvidersSetupHelpListsOnboardingCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"providers", "help"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"zero providers use <name>", "zero providers setup <catalog-id>"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected providers help to contain %q, got %q", want, output)
		}
	}
}

func writeProviderOnboardingConfig(t *testing.T, path string, cfg config.FileConfig) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// Runtime provider credentials are user-scoped even when tests inject a
// non-default config path.
func TestRunProvidersRemoveDeletesKeyFromUserStore(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	setCLIUserConfigRoot(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	seed := `{"activeProvider":"gw","providers":[{"name":"gw","provider_kind":"openai-compatible","baseURL":"https://gw.example.com/v1","apiKeyStored":true,"model":"m1"},{"name":"other","provider_kind":"openai-compatible","baseURL":"https://o.example.com/v1","model":"m2"}]}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	store, err := config.ProviderKeyStore()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("gw", "sk-secret"); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	var stdout, stderr bytes.Buffer
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}
	if code := runWithDeps([]string{"providers", "remove", "gw", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("remove failed: code=%d stderr=%s", code, stderr.String())
	}

	var payload struct {
		Removed        string `json:"removed"`
		KeyRemoved     bool   `json:"keyRemoved"`
		KeyError       string `json:"keyError"`
		ActiveProvider string `json:"activeProvider"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if payload.Removed != "gw" || !payload.KeyRemoved || payload.KeyError != "" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.ActiveProvider != "other" {
		t.Fatalf("active must hand off, got %q", payload.ActiveProvider)
	}
	if _, ok, _ := store.Get("gw"); ok {
		t.Fatalf("stored key must be deleted from the user-scoped store")
	}
}

func TestRunProvidersRemoveFailsWhenStoredKeyCleanupFails(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		name := "text"
		if jsonOutput {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("ZERO_CRED_STORAGE", "file")
			setCLIUserConfigRoot(t)
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.json")
			if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"gw","apiKeyStored":true}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := config.ProviderKeyStore()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set("gw", "sk-secret"); err != nil {
				t.Fatal(err)
			}
			// A directory at the lock-file path is a hermetic, cross-platform
			// failure: Delete cannot acquire its write lock.
			userConfigPath, err := config.DefaultUserConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(filepath.Dir(userConfigPath), "credentials.json.lock")
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(lockPath, 0o700); err != nil {
				t.Fatal(err)
			}

			args := []string{"providers", "remove", "gw"}
			if jsonOutput {
				args = append(args, "--json")
			}
			var stdout, stderr bytes.Buffer
			code := runWithDeps(args, &stdout, &stderr, appDeps{
				userConfigPath: func() (string, error) { return configPath, nil },
			})
			if code != exitCrash {
				t.Fatalf("exit = %d, want cleanup failure; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if jsonOutput {
				var payload struct {
					KeyError string `json:"keyError"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
					t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
				}
				if payload.KeyError == "" {
					t.Fatal("JSON cleanup failure omitted keyError")
				}
			} else if !strings.Contains(stderr.String(), "could not be deleted") {
				t.Fatalf("stderr = %q, want cleanup warning", stderr.String())
			}

			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
			if key, ok, getErr := store.Get("gw"); getErr != nil || !ok || key != "sk-secret" {
				t.Fatalf("failed cleanup changed key: present=%v len=%d err=%v", ok, len(key), getErr)
			}
		})
	}
}

func TestRunProvidersRemoveKeepsSharedCredentialForCaseVariantSurvivor(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	setCLIUserConfigRoot(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	seed := []byte(`{"activeProvider":"work","providers":[{"name":"work","apiKeyStored":true},{"name":"WORK","apiKeyStored":true}]}`)
	if err := os.WriteFile(configPath, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.ProviderKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("work", "sk-shared"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}
	if code := runWithDeps([]string{"providers", "remove", "WORK", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("remove failed: code=%d stderr=%s", code, stderr.String())
	}
	cfg := readFileConfig(t, configPath)
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "work" || !cfg.Providers[0].APIKeyStored {
		t.Fatalf("survivor = %+v, want credentialed work row", cfg.Providers)
	}
	if key, ok, getErr := store.Get("work"); getErr != nil || !ok || key != "sk-shared" {
		t.Fatalf("shared key = %q,%v,%v; want sk-shared,true,nil", key, ok, getErr)
	}
	var payload struct {
		KeyRemoved bool `json:"keyRemoved"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.KeyRemoved {
		t.Fatal("remove reported deleting a credential still owned by the survivor")
	}
}

func TestRunProvidersUseMatchesCredentialIdentityButNotUnicodeCaseFold(t *testing.T) {
	t.Run("case variant selects persisted spelling", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.json")
		writeProviderOnboardingConfig(t, configPath, config.FileConfig{
			ActiveProvider: "fast",
			Providers: []config.ProviderProfile{
				{Name: "OpenAI", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
				{Name: "fast", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
			},
		})
		var stdout, stderr bytes.Buffer
		if code := runWithDeps([]string{"providers", "use", "openai"}, &stdout, &stderr, providerSetupDeps(configPath)); code != exitSuccess {
			t.Fatalf("use failed: code=%d stderr=%s", code, stderr.String())
		}
		if active := readFileConfig(t, configPath).ActiveProvider; active != "OpenAI" {
			t.Fatalf("active provider = %q, want persisted spelling OpenAI", active)
		}
	})

	t.Run("environment provider accepts case variant", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-env")
		configPath := filepath.Join(t.TempDir(), "config.json")
		writeProviderOnboardingConfig(t, configPath, config.FileConfig{})
		var stdout, stderr bytes.Buffer
		if code := runWithDeps([]string{"providers", "use", "OpenAI"}, &stdout, &stderr, providerSetupDeps(configPath)); code != exitSuccess {
			t.Fatalf("environment use failed: code=%d stderr=%s", code, stderr.String())
		}
	})

	t.Run("long s is not plain s", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.json")
		writeProviderOnboardingConfig(t, configPath, config.FileConfig{
			ActiveProvider: "s",
			Providers:      []config.ProviderProfile{{Name: "s", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}},
		})
		before, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := runWithDeps([]string{"providers", "use", "ſ"}, &stdout, &stderr, providerSetupDeps(configPath)); code != exitCrash {
			t.Fatalf("use exit = %d, want crash for distinct identity; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		after, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatal("distinct Unicode identity request rewrote config")
		}
	})
}
