package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise profile construction and command planning together so dropping an
// error between discovery, finalization, and helper serialization is detected.
func TestSSHIncompleteProfileRefusesCommand(t *testing.T) {
	for _, kind := range []string{"config size", "config Include match"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
				t.Setenv(name, home)
			}
			for _, name := range []string{"ZERO_OAUTH_TOKENS_PATH", "ZERO_MCP_OAUTH_TOKENS_PATH", "GNUPGHOME", "ZERO_OAUTH_STORAGE", "CLOUDSDK_CONFIG", "GH_CONFIG_DIR", "DOCKER_CONFIG", "KUBECONFIG", "NETRC", "GOOGLE_APPLICATION_CREDENTIALS", "NPM_CONFIG_USERCONFIG", "npm_config_userconfig"} {
				t.Setenv(name, "")
			}
			sshDir := filepath.Join(home, ".ssh")
			if kind == "config size" {
				mustWriteFile(t, filepath.Join(sshDir, "config"), strings.Repeat("#", sshConfigMaxBytes+1))
			} else {
				mustWriteFile(t, filepath.Join(sshDir, "config"), "Include includes/*\n")
				for i := 0; i <= sshIncludeMatchCap; i++ {
					mustWriteFile(t, filepath.Join(sshDir, "includes", fmt.Sprintf("%03d", i)), "IdentityFile ~/relocated-key\n")
				}
			}
			workspace := t.TempDir()
			profile := PermissionProfileFromPolicy(workspace, DefaultPolicy(), nil)
			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			_, err = BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{HelperPath: helper, Config: LinuxSandboxHelperConfig{
				PermissionProfile: profile, SandboxPolicyCWD: workspace, CommandCWD: workspace, Command: []string{"true"},
			}})
			if err == nil || !strings.Contains(err.Error(), kind+" limit exceeded") {
				t.Fatalf("incomplete %s discovery allowed command planning: %v", kind, err)
			}
		})
	}
}
