package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpsertProviderTightensExistingConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX mode bits reliably")
	}

	path := filepath.Join(t.TempDir(), "zero.json")
	if err := os.WriteFile(path, []byte(`{"providers":[]}`), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	_, err := UpsertProvider(path, ProviderProfile{
		Name:         "openai",
		ProviderKind: ProviderKindOpenAI,
		APIKey:       "sk-test",
		Model:        "gpt-4.1",
	}, true)
	if err != nil {
		t.Fatalf("UpsertProvider() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 0600", got)
	}
}
