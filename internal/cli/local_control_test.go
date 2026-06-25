package cli

import (
	"encoding/json"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestCoreRegistryIncludesDefaultLocalControlTools(t *testing.T) {
	registry := newCoreRegistry(t.TempDir())
	for _, name := range []string{"browser_install", "browser_launch", "browser_connect", "browser_open", "browser_snapshot", "browser_click", "browser_type", "browser_press", "terminal_session", "capture_artifact"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if tool.Safety().Permission != tools.PermissionPrompt {
			t.Fatalf("%s permission = %s, want prompt", name, tool.Safety().Permission)
		}
	}
	desktop, ok := registry.Get("desktop_action")
	if !ok {
		t.Fatal("desktop_action not registered")
	}
	if desktop.Safety().Permission != tools.PermissionDeny {
		t.Fatalf("desktop_action permission = %s, want deny", desktop.Safety().Permission)
	}
}

func TestCoreRegistryHonorsExplicitLocalControlDisable(t *testing.T) {
	registry := newCoreRegistry(t.TempDir())
	var cfg config.LocalControlConfig
	if err := json.Unmarshal([]byte(`{"enabled":false}`), &cfg); err != nil {
		t.Fatalf("unmarshal local control config: %v", err)
	}
	registerLocalControlTools(registry, t.TempDir(), cfg)
	for _, name := range []string{"browser_install", "browser_launch", "browser_connect", "browser_open", "browser_snapshot", "browser_click", "browser_type", "browser_press", "desktop_action", "terminal_session", "capture_artifact"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if tool.Safety().Permission != tools.PermissionDeny {
			t.Fatalf("%s permission = %s, want deny", name, tool.Safety().Permission)
		}
	}
}

func TestRegisterLocalControlToolsAppliesDefaults(t *testing.T) {
	workspaceRoot := t.TempDir()
	registry := newCoreRegistry(workspaceRoot)
	registerLocalControlTools(registry, workspaceRoot, config.LocalControlConfig{Enabled: true})
	for _, name := range []string{"browser_install", "browser_launch", "browser_connect", "browser_open", "browser_snapshot", "browser_click", "browser_type", "browser_press", "terminal_session", "capture_artifact"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if tool.Safety().Permission != tools.PermissionPrompt {
			t.Fatalf("%s permission = %s, want prompt", name, tool.Safety().Permission)
		}
	}
	desktop, ok := registry.Get("desktop_action")
	if !ok {
		t.Fatal("desktop_action not registered")
	}
	if desktop.Safety().Permission != tools.PermissionDeny {
		t.Fatalf("desktop_action permission = %s, want deny without nested desktop opt-in", desktop.Safety().Permission)
	}
}
