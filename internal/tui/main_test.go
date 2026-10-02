package tui

import (
	"fmt"
	"os"
	"testing"

	"github.com/Gitlawb/zero/internal/testutil"
)

// isolatedUserDirsRoot is where TestMain points every per-user directory.
var isolatedUserDirsRoot string

// TestMain keeps the whole package off the developer's real home, config and
// cache directories. newModel loads the user's slash commands from the config
// dir, so without this every test sees (and could write to) real user state.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "zero-tui-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui tests: create isolated user dirs:", err)
		os.Exit(1)
	}
	isolatedUserDirsRoot = root
	restore := testutil.IsolateUserDirs(root)
	code := m.Run()
	restore()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
