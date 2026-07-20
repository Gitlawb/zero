package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareSandboxRuntimeStaysOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	cacheRoot := t.TempDir()
	original := sandboxUserCacheDir
	sandboxUserCacheDir = func() (string, error) { return cacheRoot, nil }
	t.Cleanup(func() { sandboxUserCacheDir = original })

	runtimeState, err := prepareSandboxRuntime(workspace)
	if err != nil {
		t.Fatalf("prepareSandboxRuntime: %v", err)
	}
	if pathWithinRoot(workspace, runtimeState.Root) {
		t.Fatalf("runtime root %q must stay outside workspace %q", runtimeState.Root, workspace)
	}
	for _, path := range []string{runtimeState.Home, runtimeState.Cache, runtimeState.Config, runtimeState.Data, runtimeState.State, runtimeState.Temp} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("managed runtime directory %q was not prepared: %v", path, err)
		}
	}
}

func TestPrepareSandboxRuntimeCleansExpiredSibling(t *testing.T) {
	workspace := t.TempDir()
	cacheRoot := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	originalCache := sandboxUserCacheDir
	originalNow := sandboxRuntimeNow
	sandboxUserCacheDir = func() (string, error) { return cacheRoot, nil }
	sandboxRuntimeNow = func() time.Time { return now }
	t.Cleanup(func() {
		sandboxUserCacheDir = originalCache
		sandboxRuntimeNow = originalNow
	})
	parent := filepath.Join(cacheRoot, "zero", "runtime", "v1")
	expired := filepath.Join(parent, "expired")
	if err := os.MkdirAll(expired, 0o700); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-sandboxRuntimeMaxAge - time.Hour)
	if err := os.Chtimes(expired, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSandboxRuntime(workspace); err != nil {
		t.Fatalf("prepareSandboxRuntime: %v", err)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatalf("expired runtime still exists: %v", err)
	}
}

func TestPrepareSandboxRuntimeFallsBackWhenUserCacheIsInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	original := sandboxUserCacheDir
	sandboxUserCacheDir = func() (string, error) { return filepath.Join(workspace, ".cache"), nil }
	t.Cleanup(func() { sandboxUserCacheDir = original })

	runtimeState, err := prepareSandboxRuntime(workspace)
	if err != nil {
		t.Fatalf("prepareSandboxRuntime: %v", err)
	}
	if pathWithinRoot(workspace, runtimeState.Root) {
		t.Fatalf("fallback runtime root %q must stay outside workspace %q", runtimeState.Root, workspace)
	}
}

func TestSandboxRuntimeEnvironmentUsesManagedState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	runtimeState := SandboxRuntime{
		Root:   root,
		Home:   filepath.Join(root, "home"),
		Cache:  filepath.Join(root, "cache"),
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		State:  filepath.Join(root, "state"),
		Temp:   filepath.Join(root, "tmp"),
	}
	env := sandboxRuntimeEnvironment([]string{
		"HOME=/workspace",
		"XDG_CACHE_HOME=/host/cache",
		"PATH=/usr/bin",
	}, &runtimeState)

	for key, want := range map[string]string{
		"HOME":                  runtimeState.Home,
		"XDG_CACHE_HOME":        runtimeState.Cache,
		"XDG_CONFIG_HOME":       runtimeState.Config,
		"XDG_DATA_HOME":         runtimeState.Data,
		"XDG_STATE_HOME":        runtimeState.State,
		"TMPDIR":                runtimeState.Temp,
		"npm_config_cache":      filepath.Join(runtimeState.Cache, "npm"),
		"NPM_CONFIG_USERCONFIG": filepath.Join(runtimeState.Config, "npmrc"),
		"YARN_CACHE_FOLDER":     filepath.Join(runtimeState.Cache, "yarn"),
		"COREPACK_HOME":         filepath.Join(runtimeState.Cache, "corepack"),
	} {
		if got := envListValue(env, key, ""); got != want {
			t.Fatalf("%s = %q, want %q; env=%#v", key, got, want, env)
		}
	}
	if got := envListValue(env, "PATH", ""); got != "/usr/bin" {
		t.Fatalf("PATH = %q, want preserved caller path", got)
	}
}

func TestEngineCommandPlanCarriesManagedRuntime(t *testing.T) {
	workspace := t.TempDir()
	cacheRoot := t.TempDir()
	original := sandboxUserCacheDir
	sandboxUserCacheDir = func() (string, error) { return cacheRoot, nil }
	t.Cleanup(func() { sandboxUserCacheDir = original })
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:            BackendLinuxBwrap,
			Available:       true,
			Platform:        "linux",
			Executable:      "/usr/bin/zero-linux-sandbox",
			CommandWrapping: true,
			NativeIsolation: true,
		},
	})

	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: workspace})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	defer plan.Cleanup()
	if plan.PermissionProfile.Runtime == nil || plan.PermissionProfile.Runtime.Root == "" {
		t.Fatal("command plan is missing managed runtime state")
	}
	if got := envListValue(plan.Env, "HOME", ""); got != plan.PermissionProfile.Runtime.Home {
		t.Fatalf("HOME = %q, want managed home %q", got, plan.PermissionProfile.Runtime.Home)
	}
	foundWriteRoot := false
	for _, root := range plan.PermissionProfile.FileSystem.WriteRoots {
		if root.Root == plan.PermissionProfile.Runtime.Root {
			foundWriteRoot = true
		}
	}
	if !foundWriteRoot {
		t.Fatalf("runtime root is not writable in profile: %#v", plan.PermissionProfile.FileSystem.WriteRoots)
	}
}
