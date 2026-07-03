package provideronboarding

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
)

func TestSetupCommandUsesCatalogEnvAndSetActive(t *testing.T) {
	groq, err := providercatalog.Require("groq")
	if err != nil {
		t.Fatalf("Require(groq) returned error: %v", err)
	}

	got := SetupCommand(groq, "fast", true)
	want := "zero providers add groq --name fast --api-key-env GROQ_API_KEY --set-active"
	if got != want {
		t.Fatalf("SetupCommand() = %q, want %q", got, want)
	}
}

func TestSetupCommandForOpenAICustomAndLocalProviders(t *testing.T) {
	tests := []struct {
		name       string
		catalogID  string
		profile    string
		setActive  bool
		want       string
		notWantArg string
	}{
		{
			name:      "openai",
			catalogID: "openai",
			profile:   "openai",
			want:      "zero providers add openai --name openai --api-key-env OPENAI_API_KEY",
		},
		{
			name:      "custom openai compatible",
			catalogID: "custom-openai-compatible",
			profile:   "custom",
			want:      "zero providers add custom-openai-compatible --name custom --api-key-env OPENAI_API_KEY",
		},
		{
			name:       "local provider",
			catalogID:  "ollama",
			profile:    "local",
			want:       "zero providers add ollama --name local",
			notWantArg: "--api-key-env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descriptor, err := providercatalog.Require(tt.catalogID)
			if err != nil {
				t.Fatalf("Require(%q) returned error: %v", tt.catalogID, err)
			}

			got := SetupCommand(descriptor, tt.profile, tt.setActive)
			if got != tt.want {
				t.Fatalf("SetupCommand() = %q, want %q", got, tt.want)
			}
			if tt.notWantArg != "" && strings.Contains(got, tt.notWantArg) {
				t.Fatalf("SetupCommand() = %q, did not want %q", got, tt.notWantArg)
			}
		})
	}
}

func TestUseAndCheckCommands(t *testing.T) {
	if got, want := UseCommand("fast"), "zero providers use fast"; got != want {
		t.Fatalf("UseCommand() = %q, want %q", got, want)
	}
	if got, want := CheckCommand("fast", false), "zero providers check fast"; got != want {
		t.Fatalf("CheckCommand(false) = %q, want %q", got, want)
	}
	if got, want := CheckCommand("fast", true), "zero providers check fast --connectivity"; got != want {
		t.Fatalf("CheckCommand(true) = %q, want %q", got, want)
	}
}

func TestMissingCredentialActionUsesCatalogEnvWithoutSecrets(t *testing.T) {
	profile := config.ProviderProfile{
		Name:      "fast",
		CatalogID: "groq",
	}

	action, ok := MissingCredentialAction(profile)
	if !ok {
		t.Fatalf("MissingCredentialAction() ok = false, want true")
	}
	if action.Label != "Set API key" {
		t.Fatalf("Label = %q, want %q", action.Label, "Set API key")
	}
	if action.Command != "set GROQ_API_KEY in your shell" {
		t.Fatalf("Command = %q, want shell-neutral env guidance", action.Command)
	}
	if !strings.Contains(action.Detail, "GROQ_API_KEY") {
		t.Fatalf("Detail = %q, want env var mention", action.Detail)
	}
	assertNoSecretLeak(t, []Action{action}, "sk-live-secret", "Bearer actual-token")
}

func TestMissingCredentialActionUsesBuiltInProviderDefaults(t *testing.T) {
	tests := []struct {
		name string
		kind config.ProviderKind
		env  string
	}{
		{name: "openai", kind: config.ProviderKindOpenAI, env: "OPENAI_API_KEY"},
		{name: "anthropic", kind: config.ProviderKindAnthropic, env: "ANTHROPIC_API_KEY"},
		{name: "google", kind: config.ProviderKindGoogle, env: "GEMINI_API_KEY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, ok := MissingCredentialAction(config.ProviderProfile{
				Name:         tt.name,
				ProviderKind: tt.kind,
			})
			if !ok {
				t.Fatalf("MissingCredentialAction() ok = false, want true")
			}
			if !strings.Contains(action.Detail, tt.env) {
				t.Fatalf("Detail = %q, want env var %q", action.Detail, tt.env)
			}
			if !strings.Contains(action.Command, tt.env) {
				t.Fatalf("Command = %q, want env var %q", action.Command, tt.env)
			}
		})
	}
}

func TestMissingCredentialActionSkipsLocalAndCredentialedProfiles(t *testing.T) {
	tests := []struct {
		name    string
		profile config.ProviderProfile
	}{
		{
			name: "local catalog provider",
			profile: config.ProviderProfile{
				Name:      "local",
				CatalogID: "ollama",
			},
		},
		{
			name: "api key is present",
			profile: config.ProviderProfile{
				Name:         "openai",
				ProviderKind: config.ProviderKindOpenAI,
				APIKey:       "sk-live-secret",
			},
		},
		{
			name: "auth header value is present",
			profile: config.ProviderProfile{
				Name:            "custom",
				CatalogID:       "custom-openai-compatible",
				AuthHeaderValue: "Bearer actual-token",
			},
		},
		{
			// providerProfileHasCredential delegates to
			// config.ProviderProfile.HasConfiguredCredential() specifically so this
			// case — a key held in the encrypted store, not in-memory on the
			// profile — isn't missed: before that delegation, onboarding advice
			// would wrongly nag for a credential this profile already has.
			name: "api key is in the encrypted store (APIKeyStored), not held in-memory",
			profile: config.ProviderProfile{
				Name:         "openai",
				ProviderKind: config.ProviderKindOpenAI,
				APIKeyStored: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, ok := MissingCredentialAction(tt.profile)
			if ok {
				t.Fatalf("MissingCredentialAction() = (%#v, true), want false", action)
			}
		})
	}
}

func TestProviderActionsIncludesExpectedActions(t *testing.T) {
	profile := config.ProviderProfile{
		Name:      "fast",
		CatalogID: "groq",
	}

	actions := ProviderActions(profile, false)
	wantCommands := []string{
		"zero providers use fast",
		"zero providers check fast",
		"set GROQ_API_KEY in your shell",
	}
	if len(actions) != len(wantCommands) {
		t.Fatalf("ProviderActions() returned %d actions, want %d: %#v", len(actions), len(wantCommands), actions)
	}
	for index, want := range wantCommands {
		if actions[index].Command != want {
			t.Fatalf("actions[%d].Command = %q, want %q", index, actions[index].Command, want)
		}
	}

	activeActions := ProviderActions(profile, true)
	for _, action := range activeActions {
		if action.Command == "zero providers use fast" {
			t.Fatalf("ProviderActions(active) included use action: %#v", activeActions)
		}
	}
}

func TestProviderActionsDoNotLeakStoredSecrets(t *testing.T) {
	const apiKey = "sk-live-secret"
	const headerValue = "Bearer actual-token"
	profile := config.ProviderProfile{
		Name:            "secure",
		ProviderKind:    config.ProviderKindOpenAI,
		APIKey:          apiKey,
		AuthHeaderValue: headerValue,
	}

	actions := ProviderActions(profile, false)
	assertNoSecretLeak(t, actions, apiKey, headerValue)
}

// TestMissingCredentialActionAuthCLISkipsProfile locks in that a CLI-authed
// profile (AuthCLI set, no key) is treated as already credentialed — without
// this, MissingCredentialAction would tell a user who chose the CLI connect
// method to go paste an API key.
func TestMissingCredentialActionAuthCLISkipsProfile(t *testing.T) {
	profile := config.ProviderProfile{
		Name:      "claude",
		CatalogID: "anthropic",
		AuthCLI:   "claude",
	}
	if _, ok := MissingCredentialAction(profile); ok {
		t.Fatal("MissingCredentialAction() ok = true, want false for an AuthCLI profile")
	}
}

// TestMissingCredentialActionWithDetectionsMentionsLoggedInCLI covers "surface
// the CLI option when the harness is detected": a profile missing a key gets
// a hint pointing at a detected, logged-in matching harness.
func TestMissingCredentialActionWithDetectionsMentionsLoggedInCLI(t *testing.T) {
	claudeHarness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("test assumption broken: claude missing from the agentcli catalog")
	}
	profile := config.ProviderProfile{Name: "anthropic", CatalogID: "anthropic"}

	// No detections at all: MissingCredentialAction (detections=nil) behaves
	// exactly as before — no CLI hint.
	before, ok := MissingCredentialAction(profile)
	if !ok {
		t.Fatal("MissingCredentialAction() ok = false, want true (no credential configured)")
	}
	if strings.Contains(before.Detail, "Claude Code") {
		t.Fatalf("Detail should not mention Claude Code with no detections: %q", before.Detail)
	}

	// A logged-in claude detection adds the hint.
	withHint, ok := MissingCredentialActionWithDetections(profile, []agentcli.Detection{
		{Harness: claudeHarness, Login: agentcli.LoggedIn},
	})
	if !ok {
		t.Fatal("MissingCredentialActionWithDetections() ok = false, want true")
	}
	if !strings.Contains(withHint.Detail, "Claude Code") {
		t.Fatalf("Detail = %q, want it to mention the detected Claude Code login", withHint.Detail)
	}

	// A logged-OUT claude detection must NOT add the hint (nothing to reuse yet).
	loggedOut, ok := MissingCredentialActionWithDetections(profile, []agentcli.Detection{
		{Harness: claudeHarness, Login: agentcli.LoggedOut},
	})
	if !ok {
		t.Fatal("MissingCredentialActionWithDetections() ok = false, want true")
	}
	if strings.Contains(loggedOut.Detail, "Claude Code") {
		t.Fatalf("Detail = %q, should not mention a logged-out CLI", loggedOut.Detail)
	}
}

func assertNoSecretLeak(t *testing.T, actions []Action, secrets ...string) {
	t.Helper()
	for _, action := range actions {
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			if strings.Contains(action.Label, secret) || strings.Contains(action.Command, secret) || strings.Contains(action.Detail, secret) {
				t.Fatalf("action leaked secret %q: %#v", secret, action)
			}
		}
	}
}
