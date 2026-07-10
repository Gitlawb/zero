package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/plugins"
)

func TestRunPluginDisableEnableJSON(t *testing.T) {
	pluginsDir := t.TempDir()
	src := writeSourcePluginDir(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
	})
	deps := appDeps{
		pluginsDir: func() string { return pluginsDir },
		loadPlugins: func(options plugins.LoadOptions) (plugins.LoadResult, error) {
			return plugins.Load(plugins.LoadOptions{Roots: []plugins.Root{{Source: plugins.SourceUser, Path: pluginsDir}}})
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugin", "add", src}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugin add exit = %d stderr=%s", exit, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "disable", "zero.demo", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugin disable exit = %d stderr=%s", exit, stderr.String())
	}
	var disabled struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &disabled); err != nil {
		t.Fatalf("disable JSON: %v\n%s", err, stdout.String())
	}
	if disabled.ID != "zero.demo" || disabled.Enabled {
		t.Fatalf("unexpected disable payload: %#v", disabled)
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "list"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugins list exit = %d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "disabled") {
		t.Fatalf("plugins list should show disabled state:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "enable", "zero.demo", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugin enable exit = %d stderr=%s", exit, stderr.String())
	}
	var enabled struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &enabled); err != nil {
		t.Fatalf("enable JSON: %v\n%s", err, stdout.String())
	}
	if enabled.ID != "zero.demo" || !enabled.Enabled {
		t.Fatalf("unexpected enable payload: %#v", enabled)
	}
}
