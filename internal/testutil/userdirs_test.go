package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolateUserDirsRedirectsAndRestores(t *testing.T) {
	// Seed distinctive values so restoring is observable, then make sure the
	// helper hands them back (set ones restored, unset ones unset again).
	t.Setenv("HOME", "before-home")
	t.Setenv("XDG_STATE_HOME", "before-state")
	os.Unsetenv("XDG_DATA_HOME")

	root := t.TempDir()
	restore := IsolateUserDirs(root)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"home": home, "config": config, "cache": cache} {
		if rel, err := filepath.Rel(root, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("%s dir %q is outside the isolated root %q", name, dir, root)
		}
	}

	restore()
	if got := os.Getenv("HOME"); got != "before-home" {
		t.Errorf("HOME after restore = %q, want before-home", got)
	}
	if got := os.Getenv("XDG_STATE_HOME"); got != "before-state" {
		t.Errorf("XDG_STATE_HOME after restore = %q, want before-state", got)
	}
	if _, set := os.LookupEnv("XDG_DATA_HOME"); set {
		t.Error("XDG_DATA_HOME was unset before and must be unset again after restore")
	}
}
