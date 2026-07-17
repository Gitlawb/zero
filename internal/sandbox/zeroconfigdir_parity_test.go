package sandbox_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sandbox"
)

// TestZeroUserConfigDirMatchesConfigUserConfigDir guards against silent drift
// between sandbox.zeroUserConfigDir and config.UserConfigDir. The sandbox copy
// exists only to avoid an import cycle (config already depends on sandbox); if
// the two diverge, deny rules would target a different directory than the
// stores write to.
func TestZeroUserConfigDirMatchesConfigUserConfigDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assertUserConfigDirParity(t)
	})

	t.Run("xdg_override", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		assertUserConfigDirParity(t)
	})

	t.Run("xdg_cleared", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		assertUserConfigDirParity(t)
		if runtime.GOOS == "darwin" {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(home, ".config")
			got, err := sandbox.ZeroUserConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("zeroUserConfigDir() = %q, want macOS ~/.config fallback %q", got, want)
			}
		}
	})
}

func assertUserConfigDirParity(t *testing.T) {
	t.Helper()
	want, err := config.UserConfigDir()
	if err != nil {
		t.Fatalf("config.UserConfigDir: %v", err)
	}
	got, err := sandbox.ZeroUserConfigDir()
	if err != nil {
		t.Fatalf("sandbox.ZeroUserConfigDir: %v", err)
	}
	if got != want {
		t.Fatalf("sandbox.ZeroUserConfigDir() = %q, config.UserConfigDir() = %q", got, want)
	}
}
