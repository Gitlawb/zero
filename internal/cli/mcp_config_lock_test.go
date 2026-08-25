package cli

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
)

// TestRunMCPAddParticipatesInConfigLock covers the half of issue #832 that
// lives outside internal/config. `zero mcp add` reads the SAME user config
// document, edits it, and republishes it with the same temp-file+rename shape
// as the config package's mutators. Locking only the config package would leave
// this writer free to clobber a concurrent provider or preference update, and
// be clobbered by one, with the file still valid JSON afterwards.
//
// Racing the two writers and hoping to observe a lost update is unreliable —
// the interleaving that loses one is narrow, and the test passed consistently
// against the unlocked code. So this asserts the property that actually matters
// and is deterministic: while the config lock is held elsewhere, the MCP
// writer's update CANNOT land. Once released it completes, and both updates
// survive.
func TestRunMCPAddParticipatesInConfigLock(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	if _, err := config.UpsertProvider(configPath, config.ProviderProfile{Name: "seed", Model: "seed-model"}, true); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	unlock, err := config.LockFile(configPath)
	if err != nil {
		t.Fatalf("acquire config lock: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			unlock()
		}
	}
	defer release()

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWithDeps([]string{"mcp", "add", "docs", "--", "docs-mcp"}, &stdout, &stderr, appDeps{
			userConfigPath: func() (string, error) { return configPath, nil },
		})
	}()

	// The MCP writer is now contending for a lock this test holds. Its write
	// must not appear until the lock is released.
	for range 20 {
		if servers := readMCPCommandConfig(t, configPath).MCP.Servers; len(servers) != 0 {
			t.Fatalf("mcp add wrote %#v while the config lock was held; it does not take the lock", servers)
		}
		select {
		case exitCode := <-done:
			t.Fatalf("mcp add completed (exit %d) while the config lock was held; it does not take the lock", exitCode)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}

	release()

	select {
	case exitCode := <-done:
		if exitCode != exitSuccess {
			t.Fatalf("mcp add exitCode = %d stderr=%s", exitCode, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("mcp add did not finish after the config lock was released")
	}

	// A config mutation after the MCP write must keep it, and vice versa.
	if _, err := config.SetTheme(configPath, "dracula"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	cfg := readMCPCommandConfig(t, configPath)
	if _, ok := cfg.MCP.Servers["docs"]; !ok {
		t.Errorf("mcp add update was lost: servers = %#v", cfg.MCP.Servers)
	}
	if cfg.Preferences.Theme != "dracula" {
		t.Errorf("theme update was lost: theme = %q, want dracula", cfg.Preferences.Theme)
	}
	if cfg.ActiveProvider != "seed" {
		t.Errorf("activeProvider = %q, want the seeded value preserved", cfg.ActiveProvider)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "seed" {
		t.Errorf("seeded provider was lost: providers = %#v", cfg.Providers)
	}
}
