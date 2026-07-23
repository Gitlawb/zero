package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInfoReturnsLockMetadataAndHashDrift(t *testing.T) {
	dir := t.TempDir()
	userRoot := filepath.Join(dir, "user")
	pluginDir := filepath.Join(userRoot, "zero.demo")
	writePluginManifest(t, pluginDir, map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Demo",
		"version":       "1.0.0",
		"enabled":       true,
		"tools":         []any{},
	})

	hash, err := hashTree(pluginDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLock(userRoot, map[string]LockEntry{
		"zero.demo": {Source: "./demo-src", Hash: hash},
	}); err != nil {
		t.Fatal(err)
	}

	info, err := Info(InfoOptions{
		LoadOptions: LoadOptions{Roots: []Root{{Source: SourceUser, Path: userRoot}}},
	}, "zero.demo")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Plugin.ID != "zero.demo" || !info.Plugin.Enabled {
		t.Fatalf("plugin = %#v", info.Plugin)
	}
	if info.LockSource != "./demo-src" || info.LockHash != hash {
		t.Fatalf("lock metadata = %#v, want source ./demo-src hash %s", info, hash)
	}
	if info.HashDrift {
		t.Fatal("expected no hash drift before manifest edit")
	}

	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"schemaVersion":1,"id":"zero.demo","name":"Demo","version":"1.0.0","enabled":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err = Info(InfoOptions{
		LoadOptions: LoadOptions{Roots: []Root{{Source: SourceUser, Path: userRoot}}},
	}, "zero.demo")
	if err != nil {
		t.Fatalf("Info after edit: %v", err)
	}
	if !info.HashDrift {
		t.Fatal("expected hash drift after manifest edit")
	}
}

func TestInfoMissingPlugin(t *testing.T) {
	_, err := Info(InfoOptions{
		LoadOptions: LoadOptions{Roots: []Root{{Source: SourceUser, Path: t.TempDir()}}},
	}, "missing")
	if err == nil {
		t.Fatal("expected missing plugin error")
	}
}
