package sandbox_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestCredentialDeniesMatchTokenStoreFallbacks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows credential deny-read is tracked separately")
	}
	workspace := t.TempDir()
	profileHome := filepath.Join(workspace, "profile-home")
	envMap := map[string]string{"HOME": "", "USERPROFILE": profileHome, "XDG_CONFIG_HOME": ""}
	oauthPath, err := oauth.ResolveStorePath(envMap)
	if err != nil {
		t.Fatal(err)
	}
	mcpPath, err := mcp.ResolveTokenStorePath(envMap)
	if err != nil {
		t.Fatal(err)
	}
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        sandbox.DefaultPolicy(),
		Backend:       sandbox.Backend{Name: sandbox.BackendUnavailable, Platform: runtime.GOOS},
	})
	plan, err := engine.BuildCommandPlan(sandbox.CommandSpec{
		Name: "true",
		Dir:  workspace,
		Env:  []string{"HOME=", "USERPROFILE=" + profileHome, "XDG_CONFIG_HOME="},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, storePath := range []string{oauthPath, mcpPath} {
		want := filepath.Dir(storePath)
		if !containsPath(plan.PermissionProfile.FileSystem.DenyReadIfExists, want) {
			t.Fatalf("DenyReadIfExists = %#v, want token-store root %q", plan.PermissionProfile.FileSystem.DenyReadIfExists, want)
		}
	}
}

func TestCredentialDeniesMatchRelativeTokenOverridesAtCommandDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows credential deny-read is tracked separately")
	}
	workspace := t.TempDir()
	commandDir := filepath.Join(workspace, "nested")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(commandDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	envMap := map[string]string{
		"HOME":                       filepath.Join(workspace, "home"),
		"ZERO_OAUTH_TOKENS_PATH":     "oauth/tokens.json",
		"ZERO_MCP_OAUTH_TOKENS_PATH": "mcp/tokens.json",
	}
	oauthPath, err := oauth.ResolveStorePath(envMap)
	if err != nil {
		t.Fatal(err)
	}
	mcpPath, err := mcp.ResolveTokenStorePath(envMap)
	if err != nil {
		t.Fatal(err)
	}
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        sandbox.DefaultPolicy(),
		Backend:       sandbox.Backend{Name: sandbox.BackendUnavailable, Platform: runtime.GOOS},
	})
	plan, err := engine.BuildCommandPlan(sandbox.CommandSpec{
		Name: "true",
		Dir:  commandDir,
		Env: []string{
			"HOME=" + envMap["HOME"],
			"ZERO_OAUTH_TOKENS_PATH=" + envMap["ZERO_OAUTH_TOKENS_PATH"],
			"ZERO_MCP_OAUTH_TOKENS_PATH=" + envMap["ZERO_MCP_OAUTH_TOKENS_PATH"],
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, storePath := range []string{oauthPath, mcpPath} {
		if !containsPath(plan.PermissionProfile.FileSystem.DenyReadIfExists, storePath) {
			t.Fatalf("DenyReadIfExists = %#v, want override path %q", plan.PermissionProfile.FileSystem.DenyReadIfExists, storePath)
		}
	}
}

func containsPath(paths []string, want string) bool {
	want = filepath.Clean(want)
	for _, path := range paths {
		if filepath.Clean(path) == want {
			return true
		}
	}
	return false
}
