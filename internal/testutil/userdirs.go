package testutil

import "os"

// userDirEnv lists every variable that os.UserHomeDir, os.UserConfigDir and
// os.UserCacheDir (and Zero's own config.UserConfigDir) read, across Linux,
// macOS and Windows. Setting only the XDG ones still leaves macOS resolving
// from HOME and Windows from USERPROFILE, APPDATA and LOCALAPPDATA.
var userDirEnv = []string{
	"HOME",
	"USERPROFILE",
	"APPDATA",
	"LOCALAPPDATA",
	"XDG_CONFIG_HOME",
	"XDG_CACHE_HOME",
	"XDG_DATA_HOME",
	"XDG_STATE_HOME",
}

// IsolateUserDirs points every per-user directory root at root so code under
// test cannot read the developer's real config, cache or home directory, or
// write to them. It is meant for a package's TestMain, which has no *testing.T
// (tests that need their own layout still use t.Setenv, which wins while it
// is active). The returned function restores the previous environment.
func IsolateUserDirs(root string) (restore func()) {
	type saved struct {
		value string
		set   bool
	}
	previous := make(map[string]saved, len(userDirEnv))
	for _, name := range userDirEnv {
		value, set := os.LookupEnv(name)
		previous[name] = saved{value: value, set: set}
		_ = os.Setenv(name, root)
	}
	return func() {
		for name, prior := range previous {
			if prior.set {
				_ = os.Setenv(name, prior.value)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}
}
