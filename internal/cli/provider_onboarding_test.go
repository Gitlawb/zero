package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

func TestRunProvidersUseSetsActiveProvider(t *testing.T) {
	t.Setenv(config.ActiveProviderEnv, "")
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

func TestRunProvidersUseExplainsRuntimeOnlyProfilesAreNotSelectable(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{Providers: []config.ProviderProfile{{Name: "saved", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}}})
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"providers", "use", "runtime"}, &stdout, &stderr, providerSetupDeps(configPath)); code != exitCrash {
		t.Fatalf("unexpected code %d", code)
	}
	if !strings.Contains(stderr.String(), `provider "runtime" not found`) {
		t.Fatalf("error missing plain not-found: %s", stderr.String())
	}
}

func TestRunProvidersUseRejectsCaseVariantOfPersistedProvider(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{
		ActiveProvider: "saved",
		Providers: []config.ProviderProfile{
			{Name: "saved", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	})

	var stdout, stderr bytes.Buffer
	deps := providerSetupDeps(configPath)
	deps.resolveConfig = func(string, config.Overrides) (config.ResolvedConfig, error) {
		profile := config.ProviderProfile{Name: "saved", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
		return config.ResolvedConfig{ActiveProvider: "saved", Provider: profile, Providers: []config.ProviderProfile{profile}}, nil
	}
	if code := runWithDeps([]string{"providers", "use", "SAVED"}, &stdout, &stderr, deps); code != exitCrash {
		t.Fatalf("exit = %d, want %d", code, exitCrash)
	}
	if !strings.Contains(stderr.String(), `provider "SAVED" not found`) {
		t.Fatalf("case-variant error was not plain not-found: %q", stderr.String())
	}
	if cfg := readFileConfig(t, configPath); cfg.ActiveProvider != "saved" {
		t.Fatalf("ActiveProvider = %q, want saved", cfg.ActiveProvider)
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

// providersUseOverrideConfigAtDefaultUserPath is providersUseOverrideConfig,
// but written to the exact path config.DefaultUserConfigPath() resolves to via
// a redirected APPDATA/XDG_CONFIG_HOME.
func providersUseOverrideConfigAtDefaultUserPath(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", root)
	} else {
		t.Setenv("XDG_CONFIG_HOME", root)
	}
	configPath := filepath.Join(root, "zero", "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{
		ActiveProvider: "work",
		Providers: []config.ProviderProfile{
			{Name: "work", ProviderKind: config.ProviderKindOpenAI, BaseURL: config.OpenAIBaseURL, Model: "gpt-4.1"},
			{Name: "fast", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", Model: "llama-3.3-70b-versatile"},
		},
	})
	return configPath
}

// TestRunProvidersUseResolvesCaseVariantEnvOverride is the regression test for
// jatmn's #725 review: the override check ran the real resolver but compared its
// result to the raw env string exactly. Resolution matches the active row
// case-insensitively and reports the row's persisted spelling, so
// ZERO_PROVIDER=WORK against a saved "work" resolves fine at runtime yet was
// reported as unresolvable — telling the user Zero could not start on an
// override that works.
func TestRunProvidersUseResolvesCaseVariantEnvOverride(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// As in TestRunProvidersUseJSONFlagsEnvOverride: the resolver reads the real
	// process environment, so the override has to be set for real.
	t.Setenv(config.ActiveProviderEnv, "WORK")
	deps := providerSetupDeps(providersUseOverrideConfigAtDefaultUserPath(t))
	deps.getenv = func(key string) string {
		if key == config.ActiveProviderEnv {
			return "WORK"
		}
		return ""
	}

	if code := runWithDeps([]string{"providers", "use", "fast", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("JSON did not decode: %v\n%s", err, stdout.String())
	}
	if resolves, ok := payload["envProviderResolves"].(bool); !ok || !resolves {
		t.Fatalf("envProviderResolves = %#v, want true for a case-variant override of a saved profile", payload["envProviderResolves"])
	}
	if payload["effectiveProvider"] != "WORK" {
		t.Fatalf("effectiveProvider = %#v, want the override reported as effective", payload["effectiveProvider"])
	}

	stdout.Reset()
	stderr.Reset()
	textDeps := providerSetupDeps(providersUseOverrideConfigAtDefaultUserPath(t))
	textDeps.getenv = deps.getenv
	if code := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, textDeps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	if note := stderr.String(); strings.Contains(note, "can be resolved") {
		t.Fatalf("a resolvable case-variant override must not be reported as broken: %q", note)
	}
}

// TestRunProvidersUseRejectsCatalogIDOfSavedProfile covers jatmn's #725 finding
// that catalog-id addressing of a SAVED row took the runtime-only path: the row
// is not persisted under that name, but the config plainly owns the identity, so
// the env-derived explanation is false and exiting 0 hides a failed switch.
func TestRunProvidersUseRejectsCatalogIDOfSavedProfile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{
		ActiveProvider: "work",
		Providers: []config.ProviderProfile{
			{Name: "work", ProviderKind: config.ProviderKindOpenAI, BaseURL: config.OpenAIBaseURL, Model: "gpt-4.1"},
			{Name: "my-router", ProviderKind: config.ProviderKindOpenAICompatible, CatalogID: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Model: "x"},
		},
	})

	var stdout, stderr bytes.Buffer
	deps := providerSetupDeps(configPath)
	deps.resolveConfig = func(string, config.Overrides) (config.ResolvedConfig, error) {
		saved := config.ProviderProfile{Name: "my-router", ProviderKind: config.ProviderKindOpenAICompatible, CatalogID: "openrouter", Model: "x"}
		return config.ResolvedConfig{ActiveProvider: "work", Provider: saved, Providers: []config.ProviderProfile{saved}}, nil
	}
	if code := runWithDeps([]string{"providers", "use", "openrouter"}, &stdout, &stderr, deps); code != exitCrash {
		t.Fatalf("exit = %d, want %d (stdout %q, stderr %q)", code, exitCrash, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "environment variable") {
		t.Fatalf("a saved profile addressed by catalog id must not be described as environment-derived: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `provider "openrouter" not found`) {
		t.Fatalf("stderr = %q, want the real not-found error", stderr.String())
	}
	if !strings.Contains(stderr.String(), `"my-router"`) {
		t.Fatalf("stderr = %q, want the saved profile's name named in the hint", stderr.String())
	}
	if cfg := readFileConfig(t, configPath); cfg.ActiveProvider != "work" {
		t.Fatalf("ActiveProvider = %q, want work (nothing should have switched)", cfg.ActiveProvider)
	}
}

// TestRunProvidersRemoveRenameRejectCaseVariantOfPersistedProvider extends the
// guard `providers use` already had to remove and rename, which jatmn found had
// been left behind: `zero providers remove SAVED` against a saved "saved" exited
// 0 with the environment-variable explanation instead of failing not-found.
func TestRunProvidersRemoveRenameRejectCaseVariantOfPersistedProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "remove", args: []string{"providers", "remove", "SAVED"}},
		{name: "rename", args: []string{"providers", "rename", "SAVED", "renamed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.json")
			writeProviderOnboardingConfig(t, configPath, config.FileConfig{
				ActiveProvider: "saved",
				Providers: []config.ProviderProfile{
					{Name: "saved", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
				},
			})

			var stdout, stderr bytes.Buffer
			deps := providerSetupDeps(configPath)
			deps.resolveConfig = func(string, config.Overrides) (config.ResolvedConfig, error) {
				profile := config.ProviderProfile{Name: "saved", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
				return config.ResolvedConfig{ActiveProvider: "saved", Provider: profile, Providers: []config.ProviderProfile{profile}}, nil
			}
			if code := runWithDeps(tc.args, &stdout, &stderr, deps); code != exitCrash {
				t.Fatalf("exit = %d, want %d (stdout %q, stderr %q)", code, exitCrash, stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String(), "environment variable") {
				t.Fatalf("a case variant of a saved profile must not be described as environment-derived: %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), `provider "SAVED" not found`) {
				t.Fatalf("stderr = %q, want the real not-found error", stderr.String())
			}
			if !strings.Contains(stderr.String(), `"saved"`) {
				t.Fatalf("stderr = %q, want the persisted spelling named in the hint", stderr.String())
			}
			cfg := readFileConfig(t, configPath)
			if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "saved" {
				t.Fatalf("config was mutated by a rejected command: %#v", cfg.Providers)
			}
		})
	}
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
	// activeProviderEnvOverrideResolution runs the resolver to prove the override
	// is genuinely effective, and the resolver reads the real process
	// environment (config.Resolve falls back to os.Getenv when no Env map is
	// injected) — so the override must be set for real, not just mocked via
	// deps.getenv, which only feeds the separate "is this an override at all"
	// check.
	t.Setenv(config.ActiveProviderEnv, "work")
	deps := providerSetupDeps(providersUseOverrideConfigAtDefaultUserPath(t))
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

// A ZERO_PROVIDER value that names nothing resolvable must not be reported as
// the effective provider: the next resolution fails on it, so the note has to say
// the override is broken rather than send the user to check a provider that does
// not exist.
func TestRunProvidersUseFlagsUnresolvableEnvOverride(t *testing.T) {
	configPath := providersUseOverrideConfig(t)
	getenv := func(key string) string {
		if key == config.ActiveProviderEnv {
			return "removed-profile"
		}
		return ""
	}

	var stdout, stderr bytes.Buffer
	deps := providerSetupDeps(configPath)
	deps.getenv = getenv
	if code := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	note := stderr.String()
	for _, want := range []string{config.ActiveProviderEnv, "removed-profile", "can be resolved"} {
		if !strings.Contains(note, want) {
			t.Fatalf("unresolvable-override note missing %q, got %q", want, note)
		}
	}
	if strings.Contains(note, "stays the active provider") {
		t.Fatalf("an unresolvable override must not be called the active provider: %q", note)
	}

	stdout.Reset()
	stderr.Reset()
	jsonDeps := providerSetupDeps(configPath)
	jsonDeps.getenv = getenv
	if code := runWithDeps([]string{"providers", "use", "fast", "--json"}, &stdout, &stderr, jsonDeps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("JSON did not decode: %v\n%s", err, stdout.String())
	}
	if _, reported := payload["effectiveProvider"]; reported {
		t.Fatalf("an unresolvable override must not be reported as effective: %#v", payload)
	}
	if payload["envProvider"] != "removed-profile" || payload["overriddenByEnv"] != config.ActiveProviderEnv {
		t.Fatalf("JSON must still name the override, got %#v", payload)
	}
	if resolves, ok := payload["envProviderResolves"].(bool); !ok || resolves {
		t.Fatalf("envProviderResolves = %#v, want false", payload["envProviderResolves"])
	}
}

// A ZERO_PROVIDER override that names a persisted profile is still not proof
// the next resolution succeeds: an OpenAI-compatible profile saved without a
// model fails normalization (config.Resolve requires one), so the override
// must not be reported as effective just because a config.json row exists.
func TestRunProvidersUseFlagsBrokenPersistedEnvOverride(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", root)
	} else {
		t.Setenv("XDG_CONFIG_HOME", root)
	}
	configPath := filepath.Join(root, "zero", "config.json")
	writeProviderOnboardingConfig(t, configPath, config.FileConfig{
		ActiveProvider: "work",
		Providers: []config.ProviderProfile{
			{Name: "work", ProviderKind: config.ProviderKindOpenAI, BaseURL: config.OpenAIBaseURL, Model: "gpt-4.1"},
			{Name: "fast", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", Model: "llama-3.3-70b-versatile"},
			{Name: "broken", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://api.example.com/v1"},
		},
	})
	t.Setenv(config.ActiveProviderEnv, "broken")
	deps := providerSetupDeps(configPath)
	deps.getenv = func(key string) string {
		if key == config.ActiveProviderEnv {
			return "broken"
		}
		return ""
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"providers", "use", "fast", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("JSON did not decode: %v\n%s", err, stdout.String())
	}
	if _, reported := payload["effectiveProvider"]; reported {
		t.Fatalf("a persisted-but-unresolvable override must not be reported as effective: %#v", payload)
	}
	if resolves, ok := payload["envProviderResolves"].(bool); !ok || resolves {
		t.Fatalf("envProviderResolves = %#v, want false", payload["envProviderResolves"])
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	if strings.Contains(stderr.String(), "stays the active provider") {
		t.Fatalf("a persisted-but-unresolvable override must not be called the active provider: %q", stderr.String())
	}
}

func TestRunProvidersUseDoesNotBlameOverrideForInvalidProjectConfig(t *testing.T) {
	t.Setenv(config.ActiveProviderEnv, "work")
	configPath := providersUseOverrideConfigAtDefaultUserPath(t)
	workspace := t.TempDir()
	projectPath := filepath.Join(workspace, ".zero", "config.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"tools":{"deferThreshold":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := providerSetupDeps(configPath)
	deps.getwd = func() (string, error) { return workspace, nil }
	deps.getenv = func(key string) string {
		if key == config.ActiveProviderEnv {
			return "work"
		}
		return ""
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	note := stderr.String()
	if !strings.Contains(note, "configuration is invalid") || !strings.Contains(note, "deferThreshold") {
		t.Fatalf("note did not report the actual config error: %q", note)
	}
	if strings.Contains(note, "no provider named work") {
		t.Fatalf("unrelated config failure was blamed on ZERO_PROVIDER: %q", note)
	}
}

func TestRunProvidersUseDefersOverrideResolutionWhenProviderCommandIsSet(t *testing.T) {
	configPath := providersUseOverrideConfig(t)
	marker := filepath.Join(t.TempDir(), "provider-command-ran")
	t.Setenv(config.ActiveProviderEnv, "work")
	t.Setenv("ZERO_TEST_PROVIDER_COMMAND_MARKER", marker)
	providerCommand := strconv.Quote(os.Args[0]) + " -test.run=^TestProviderCommandSentinel$"
	t.Setenv(config.ProviderCommandEnv, providerCommand)
	deps := providerSetupDeps(configPath)
	deps.getenv = func(key string) string {
		switch key {
		case config.ActiveProviderEnv:
			return "work"
		case config.ProviderCommandEnv:
			return providerCommand
		default:
			return ""
		}
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"providers", "use", "fast", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("JSON did not decode: %v\n%s", err, stdout.String())
	}
	if payload["envProviderResolution"] != "deferred" || payload["envProviderResolves"] != nil {
		t.Fatalf("provider-command override resolution must be deferred: %#v", payload)
	}
	if _, reported := payload["effectiveProvider"]; reported {
		t.Fatalf("deferred override must not be reported as effective: %#v", payload)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("provider command ran during override reporting: stat error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"providers", "use", "fast"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
	}
	for _, want := range []string{config.ProviderCommandEnv, "determined", "next resolves configuration"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("deferred override note missing %q: %q", want, stderr.String())
		}
	}
}

func TestProviderCommandSentinel(t *testing.T) {
	marker := os.Getenv("ZERO_TEST_PROVIDER_COMMAND_MARKER")
	if marker == "" {
		return
	}
	if err := os.WriteFile(marker, []byte("ran"), 0o600); err != nil {
		t.Fatalf("write provider-command marker: %v", err)
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

// TestActiveProviderEnvOverrideReportsEveryDistinctSpelling pins the pure
// function's contract after jatmn's #725 round: a case-only difference is no
// longer silently treated as "the same provider". Deciding that needs the
// resolver (see TestRunProvidersUseCaseVariantEnvOverrideFollowsResolution),
// so this layer reports every spelling that is not literally the selection.
func TestActiveProviderEnvOverrideReportsEveryDistinctSpelling(t *testing.T) {
	getenv := func(value string) func(string) string {
		return func(key string) string {
			if key == config.ActiveProviderEnv {
				return value
			}
			return ""
		}
	}
	if override := activeProviderEnvOverride(getenv("work"), "work"); override != "" {
		t.Fatalf("activeProviderEnvOverride() = %q, want no override when the env names the selection exactly", override)
	}
	if override := activeProviderEnvOverride(getenv("WORK"), "work"); override != "WORK" {
		t.Fatalf("activeProviderEnvOverride() = %q, want the case-distinct spelling reported to the resolver", override)
	}
	if override := activeProviderEnvOverride(getenv("fast"), "work"); override != "fast" {
		t.Fatalf("activeProviderEnvOverride() = %q, want the genuinely different provider reported", override)
	}
}

// TestRunProvidersUseCaseVariantEnvOverrideFollowsResolution is the regression
// test for jatmn's #725 finding that a case-distinct ZERO_PROVIDER was hidden
// unconditionally. Folding is right only when the env value lands on the row
// `providers use` just wrote; a project config contributing a separate
// exact-case "WORK" profile makes it a real override of a different provider,
// with different credentials, and that must still be reported.
func TestRunProvidersUseCaseVariantEnvOverrideFollowsResolution(t *testing.T) {
	run := func(t *testing.T, projectProviders []config.ProviderProfile) string {
		t.Helper()
		// The resolver reads the real process environment, so the override has
		// to be set for real, and the user config has to sit at the default path.
		t.Setenv(config.ActiveProviderEnv, "WORK")
		configPath := providersUseOverrideConfigAtDefaultUserPath(t)
		workspace := t.TempDir()
		if len(projectProviders) > 0 {
			projectPath := filepath.Join(workspace, ".zero", "config.json")
			writeProviderOnboardingConfig(t, projectPath, config.FileConfig{Providers: projectProviders})
		}
		deps := providerSetupDeps(configPath)
		deps.getwd = func() (string, error) { return workspace, nil }
		deps.getenv = func(key string) string {
			if key == config.ActiveProviderEnv {
				return "WORK"
			}
			return ""
		}
		var stdout, stderr bytes.Buffer
		if code := runWithDeps([]string{"providers", "use", "work"}, &stdout, &stderr, deps); code != exitSuccess {
			t.Fatalf("exit = %d, want %d: %s", code, exitSuccess, stderr.String())
		}
		return stderr.String()
	}

	t.Run("case variant of the selection", func(t *testing.T) {
		if note := run(t, nil); note != "" {
			t.Fatalf("an override that resolves to the selected row must not warn: %q", note)
		}
	})

	t.Run("case-distinct project profile", func(t *testing.T) {
		note := run(t, []config.ProviderProfile{{
			Name:         "WORK",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://api.example.com/v1",
			Model:        "example-model",
		}})
		if !strings.Contains(note, "WORK") || !strings.Contains(note, config.ActiveProviderEnv) {
			t.Fatalf("a separate exact-case profile must be reported as an override: %q", note)
		}
	})
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

// TestRunProvidersRemoveDeletesKeyBesideConfig: the stored key must be deleted
// from the credential store CO-LOCATED with the config being edited (where
// SecureProviderProfile captured it), not the default-path store.
func TestRunProvidersRemoveDeletesKeyBesideConfig(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	seed := `{"activeProvider":"gw","providers":[{"name":"gw","catalogId":"catalog-gw","provider_kind":"openai-compatible","baseURL":"https://gw.example.com/v1","apiKeyStored":true,"model":"m1"},{"name":"other","provider_kind":"openai-compatible","baseURL":"https://o.example.com/v1","model":"m2"}]}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	store, err := config.ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("catalog-gw", "sk-secret"); err != nil {
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
	if _, ok, _ := store.Get("catalog-gw"); ok {
		t.Fatalf("stored key under the catalog alias must be deleted from the store beside the config")
	}
}

func TestProviderResolvedByNameRequiresOneCredentialStoreIdentity(t *testing.T) {
	providers := []config.ProviderProfile{
		{Name: "work-xai", CatalogID: "xai"},
		{Name: "personal-xai", CatalogID: "xai"},
	}
	if providerResolvedByName(providers, "xai") {
		t.Fatal("a shared catalog id must not be attributed to the first resolved provider")
	}
	if providerResolvedByName([]config.ProviderProfile{{Name: "s"}}, "ſ") {
		t.Fatal("Unicode long-s is distinct from the credential store identity s")
	}
	if !providerResolvedByName([]config.ProviderProfile{{Name: "work"}}, "WORK") {
		t.Fatal("ASCII case variants should resolve to the same credential-store identity")
	}
}

func TestRunProvidersRemoveCaseDuplicateKeepsSurvivorKey(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	seed := `{"activeProvider":"work","providers":[{"name":"work","apiKeyStored":true},{"name":"WORK"}]}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("work", "survivor-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}
	if code := runWithDeps([]string{"providers", "remove", "WORK", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("remove failed: code=%d stderr=%s", code, stderr.String())
	}
	if key, ok, err := store.Get("work"); err != nil || !ok || key != "survivor-key" {
		t.Fatalf("surviving provider key = %q, %v, %v; want retained", key, ok, err)
	}
	var payload struct {
		KeyRemoved bool `json:"keyRemoved"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil || payload.KeyRemoved {
		t.Fatalf("payload = %s, err = %v; shared key must not be reported removed", stdout.String(), err)
	}
}

// TestRunProvidersRemoveTransfersKeyMarkerToSurvivingCaseVariant covers the
// inverse of TestRunProvidersRemoveCaseDuplicateKeepsSurvivorKey: the removed
// row (not the survivor) owns apiKeyStored. Without transferring the marker,
// the still-shared credential-store secret becomes unreachable through the
// surviving row even though it was never deleted.
func TestRunProvidersRemoveTransfersKeyMarkerToSurvivingCaseVariant(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	seed := `{"activeProvider":"WORK","providers":[{"name":"work","catalogId":"xai","apiKeyStored":true},{"name":"WORK"}]}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("work", "shared-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("xai", "alias-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}
	if code := runWithDeps([]string{"providers", "remove", "work", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("remove failed: code=%d stderr=%s", code, stderr.String())
	}
	if key, ok, err := store.Get("work"); err != nil || !ok || key != "shared-key" {
		t.Fatalf("shared provider key = %q, %v, %v; want retained", key, ok, err)
	}
	if _, ok, err := store.Get("xai"); err != nil || ok {
		t.Fatalf("exclusive catalog-alias key survived removal: ok=%v err=%v", ok, err)
	}
	var payload struct {
		KeyRemoved bool `json:"keyRemoved"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil || !payload.KeyRemoved {
		t.Fatalf("payload = %s, err = %v; deleted catalog-alias key must be reported removed", stdout.String(), err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.FileConfig
	if err := json.Unmarshal(after, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "WORK" || !cfg.Providers[0].APIKeyStored {
		t.Fatalf("survivor must inherit apiKeyStored marker, got %+v", cfg.Providers)
	}
}

// TestRunProvidersRemoveUsesCredentialStoreEquivalenceForSurvivors covers
// jatmn's #725 finding that survivor detection compared with strings.EqualFold
// while the credential store keys entries with trimmed strings.ToLower. The two
// relations differ: Unicode case folding equates "s" and "ſ", ToLower does not.
// Both spellings therefore pass persisted-name validation as separate rows with
// separate store entries, but the EqualFold check called "ſ" a survivor of
// removing "s" — skipping the key deletion (orphaning the secret) and handing
// the marker to a row whose lookup uses a different entry entirely.
func TestRunProvidersRemoveUsesCredentialStoreEquivalenceForSurvivors(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	seed := `{"activeProvider":"s","providers":[{"name":"s","apiKeyStored":true},{"name":"\u017f"}]}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("s", "long-s-is-not-s"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}
	if code := runWithDeps([]string{"providers", "remove", "s", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("remove failed: code=%d stderr=%s", code, stderr.String())
	}
	if _, ok, err := store.Get("s"); err != nil || ok {
		t.Fatalf("key = ok:%v err:%v; the removed row owned its own store entry, which must be deleted", ok, err)
	}
	var payload struct {
		KeyRemoved bool `json:"keyRemoved"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil || !payload.KeyRemoved {
		t.Fatalf("payload = %s, err = %v; want keyRemoved true", stdout.String(), err)
	}
	cfg := readFileConfig(t, configPath)
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "\u017f" {
		t.Fatalf("providers = %+v, want only the distinct long-s row", cfg.Providers)
	}
	if cfg.Providers[0].APIKeyStored {
		t.Fatal("a row with its own credential-store identity must not inherit another row's marker")
	}
}
