package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadLockDecodesMarketplaceFieldsAdditively(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{
  "zero.demo": {
    "source": "https://github.com/Gitlawb/zero-demo-plugin.git",
    "hash": "sha256:abc",
    "catalog": "official",
    "version": "1.2.3",
    "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "subdir": "plugins/demo",
    "enabled": false,
    "pinned": true
  }
}`)
	if err := os.WriteFile(filepath.Join(dir, LockFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}

	lock, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	entry := lock["zero.demo"]
	if entry.Catalog != "official" || entry.Version != "1.2.3" || entry.Commit != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || entry.Subdir != "plugins/demo" || entry.Enabled == nil || *entry.Enabled || !entry.Pinned {
		t.Fatalf("marketplace fields did not decode: %#v", entry)
	}
}

func TestDisableMovesPluginToQuarantineAndLoaderListsDisabled(t *testing.T) {
	root := t.TempDir()
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: root}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := Disable(root, "zero.demo"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "zero.demo")); !os.IsNotExist(err) {
		t.Fatalf("active plugin dir should be absent after disable, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, disabledDirName, "zero.demo", manifestFileName)); err != nil {
		t.Fatalf("quarantined plugin manifest missing: %v", err)
	}
	lock, err := ReadLock(root)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock["zero.demo"].Enabled == nil || *lock["zero.demo"].Enabled {
		t.Fatalf("lock entry should record enabled:false: %#v", lock["zero.demo"])
	}

	loaded, err := Load(LoadOptions{Roots: []Root{{Source: SourceUser, Path: root}}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Plugins) != 1 || loaded.Plugins[0].ID != "zero.demo" || loaded.Plugins[0].Enabled {
		t.Fatalf("disabled plugin should be listed but inactive: %#v", loaded.Plugins)
	}
}

func TestEnableMovesQuarantinedPluginBackToActiveRoot(t *testing.T) {
	root := t.TempDir()
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: root}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Disable(root, "zero.demo"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	if err := Enable(root, "zero.demo"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "zero.demo", manifestFileName)); err != nil {
		t.Fatalf("active plugin manifest missing after enable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, disabledDirName, "zero.demo")); !os.IsNotExist(err) {
		t.Fatalf("quarantine dir should be absent after enable, err=%v", err)
	}
	lock, err := ReadLock(root)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock["zero.demo"].Enabled == nil || !*lock["zero.demo"].Enabled {
		t.Fatalf("lock entry should record enabled:true: %#v", lock["zero.demo"])
	}
}

func TestInstallPreservesDisabledStateThroughUpdate(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	writeSourcePlugin(t, src, validManifest())
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: root}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Disable(root, "zero.demo"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	bumped := validManifest()
	bumped["version"] = "0.2.0"
	writeSourcePlugin(t, src, bumped)
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: root}); err != nil {
		t.Fatalf("disabled update install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "zero.demo")); !os.IsNotExist(err) {
		t.Fatalf("active plugin dir should remain absent after disabled update, err=%v", err)
	}
	loaded, err := Load(LoadOptions{Roots: []Root{{Source: SourceUser, Path: root}}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Plugins) != 1 || loaded.Plugins[0].Version != "0.2.0" || loaded.Plugins[0].Enabled {
		t.Fatalf("disabled update should stay quarantined at new version: %#v", loaded.Plugins)
	}
	lock, err := ReadLock(root)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock["zero.demo"].Enabled == nil || *lock["zero.demo"].Enabled {
		t.Fatalf("lock should preserve enabled:false: %#v", lock["zero.demo"])
	}
}

func TestDisabledProjectPluginShadowsUserPlugin(t *testing.T) {
	dir := t.TempDir()
	userRoot := filepath.Join(dir, "user")
	projectRoot := filepath.Join(dir, "project")
	writePluginManifest(t, filepath.Join(userRoot, "demo"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "User Demo",
		"version":       "0.1.0",
	})
	writePluginManifest(t, filepath.Join(projectRoot, disabledDirName, "demo"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Project Demo",
		"version":       "0.2.0",
	})

	result, err := Load(LoadOptions{
		Roots: []Root{
			{Source: SourceUser, Path: userRoot},
			{Source: SourceProject, Path: projectRoot},
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result.Plugins) != 1 || result.Plugins[0].Name != "Project Demo" || result.Plugins[0].Enabled {
		t.Fatalf("disabled project plugin should shadow user plugin: %#v", result.Plugins)
	}
}
