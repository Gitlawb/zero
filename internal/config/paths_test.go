package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultResolveOptionsUsesExistingConfigPathsAndProviderCommand(t *testing.T) {
	userConfigRoot := setUserConfigRoot(t)
	workspaceRoot := t.TempDir()
	userPath := filepath.Join(userConfigRoot, "zero", "config.json")
	projectPath := filepath.Join(workspaceRoot, ".zero", "config.json")
	writeFileAt(t, userPath, "{}")
	writeFileAt(t, projectPath, "{}")
	t.Setenv("ZERO_PROVIDER_COMMAND", "  zero-provider  ")

	options, err := DefaultResolveOptions(workspaceRoot)
	if err != nil {
		t.Fatalf("DefaultResolveOptions() error = %v", err)
	}

	if options.UserConfigPath != userPath {
		t.Fatalf("UserConfigPath = %q, want %q", options.UserConfigPath, userPath)
	}
	if options.ProjectConfigPath != projectPath {
		t.Fatalf("ProjectConfigPath = %q, want %q", options.ProjectConfigPath, projectPath)
	}
	if options.ProviderCommand != "zero-provider" {
		t.Fatalf("ProviderCommand = %q, want zero-provider", options.ProviderCommand)
	}
}

func TestDefaultResolveOptionsIgnoresMissingConfigFiles(t *testing.T) {
	setUserConfigRoot(t)
	workspaceRoot := t.TempDir()

	options, err := DefaultResolveOptions(workspaceRoot)
	if err != nil {
		t.Fatalf("DefaultResolveOptions() error = %v", err)
	}

	if options.UserConfigPath != "" {
		t.Fatalf("UserConfigPath = %q, want empty for missing file", options.UserConfigPath)
	}
	if options.ProjectConfigPath != "" {
		t.Fatalf("ProjectConfigPath = %q, want empty for missing file", options.ProjectConfigPath)
	}
}

func TestDefaultResolveOptionsRejectsDirectoryConfigPaths(t *testing.T) {
	tests := []struct {
		name          string
		makeDirectory func(t *testing.T, userConfigRoot string, workspaceRoot string) string
	}{
		{
			name: "user config",
			makeDirectory: func(t *testing.T, userConfigRoot string, workspaceRoot string) string {
				t.Helper()
				path := filepath.Join(userConfigRoot, "zero", "config.json")
				mkdirAll(t, path)
				return path
			},
		},
		{
			name: "project config",
			makeDirectory: func(t *testing.T, userConfigRoot string, workspaceRoot string) string {
				t.Helper()
				path := filepath.Join(workspaceRoot, ".zero", "config.json")
				mkdirAll(t, path)
				return path
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			userConfigRoot := setUserConfigRoot(t)
			workspaceRoot := t.TempDir()
			path := tc.makeDirectory(t, userConfigRoot, workspaceRoot)

			_, err := DefaultResolveOptions(workspaceRoot)
			if err == nil {
				t.Fatal("DefaultResolveOptions() error = nil, want directory error")
			}
			if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "is a directory") {
				t.Fatalf("error = %q, want clear directory message for %q", err.Error(), path)
			}
		})
	}
}

func setUserConfigRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", root)
	case "darwin":
		t.Setenv("HOME", root)
	default:
		t.Setenv("XDG_CONFIG_HOME", root)
	}

	configRoot, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir() error = %v", err)
	}
	return configRoot
}

func writeFileAt(t *testing.T, path string, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent directories: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("create directory %s: %v", path, err)
	}
}
