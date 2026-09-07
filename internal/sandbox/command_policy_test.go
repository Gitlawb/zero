package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// testPolicyWithSSHDirectoryDeny gives command-planning tests a maskable SSH
// policy so they reach their intended network, runtime, or other credential
// checks. Supplied homes must belong to the test; otherwise create an isolated
// home and redirect all credential roots before constructing the profile.
func testPolicyWithSSHDirectoryDeny(t *testing.T, homes ...string) Policy {
	t.Helper()
	if len(homes) == 0 {
		home := t.TempDir()
		homes = []string{home}
		for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
			t.Setenv(name, home)
		}
	}
	for _, name := range []string{"ZERO_OAUTH_TOKENS_PATH", "ZERO_MCP_OAUTH_TOKENS_PATH", "GNUPGHOME", "ZERO_OAUTH_STORAGE", "CLOUDSDK_CONFIG", "GH_CONFIG_DIR", "DOCKER_CONFIG", "KUBECONFIG", "NETRC", "GOOGLE_APPLICATION_CREDENTIALS", "NPM_CONFIG_USERCONFIG", "npm_config_userconfig"} {
		t.Setenv(name, "")
	}
	policy := DefaultPolicy()
	for _, home := range homes {
		sshDir := filepath.Join(home, ".ssh")
		if err := os.MkdirAll(sshDir, 0700); err != nil {
			t.Fatal(err)
		}
		policy.DenyRead = append(policy.DenyRead, sshDir)
	}
	return policy
}
