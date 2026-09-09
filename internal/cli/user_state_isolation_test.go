package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

// isolateCLIUserState points every user-scoped path the root command touches at
// throwaway directories, and keeps credential work off the host keyring.
//
// A TEMPORARY WORKING DIRECTORY IS NOT ISOLATION. runWithDeps fills the
// dependencies a test leaves out with the production ones, and the interactive
// launch path reads user config, opens stores, refreshes the models.dev cache
// and migrates any inline plaintext API key into the credential store before it
// ever reaches an injected runTUI callback. Without this, running these tests on
// a developer machine rewrites that developer's config.json and moves their key
// into their keychain, and the results depend on whatever providers, MCP servers
// and plugins that machine has configured.
func isolateCLIUserState(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	// One root behind every per-user base directory, so the platform-specific
	// resolution lands inside it whichever branch it takes.
	for _, name := range []string{
		"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
	} {
		t.Setenv(name, root)
	}
	// The models.dev overlay cache, and the background refresh that writes it:
	// a wiring test has no business making a network call or leaving a cache
	// file behind.
	t.Setenv("ZERO_MODELS_CACHE_PATH", filepath.Join(root, "modelsdev.json"))
	t.Setenv("ZERO_DISABLE_MODELS_FETCH", "1")
	// Credentials resolve keyring-first. This keeps a migrated key in a file
	// under the fixture root rather than in the developer's OS keychain.
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
}

// AND THE ISOLATION IS ITSELF PINNED. A seeded config outside the helper's own
// fixture root stands in for a developer's real one: the launch helpers must
// leave it exactly as they found it, inline API key included, and must not
// write a credential file beside it.
func TestCLILaunchHelpersLeaveUserStateAlone(t *testing.T) {
	seedRoot := t.TempDir()
	for _, name := range []string{
		"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
	} {
		t.Setenv(name, seedRoot)
	}
	configPath, err := config.DefaultUserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.HasPrefix(configPath, seedRoot) {
		t.Fatalf("SETUP INVALID: user config resolved to %q, outside the seeded root %q", configPath, seedRoot)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seeded := []byte(`{"providers":[{"name":"anthropic","providerKind":"anthropic","model":"claude-haiku-4.5","apiKey":"sk-seeded-plaintext-key"}]}`)
	if err := os.WriteFile(configPath, seeded, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}

	// Both helpers added by this PR, each of which reaches the real launch path.
	_ = captureTUIOptions(t, "--allow-escalation")
	_ = execAdvertisesEscalateModel(t, []string{"--allow-escalation", "exec", "say hi"})

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(seeded) {
		t.Errorf("the seeded user config was rewritten:\n before: %s\n  after: %s", seeded, after)
	}
	entries, err := os.ReadDir(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(before) {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("the user config directory gained entries: %v", names)
	}
}
