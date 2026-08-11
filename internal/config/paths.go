package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const ProviderCommandEnv = "ZERO_PROVIDER_COMMAND"

// DefaultResolveOptions builds config resolution inputs from the local process
// environment and workspace.
func DefaultResolveOptions(workspaceRoot string) (ResolveOptions, error) {
	userConfigPath, err := DefaultUserConfigPath()
	if err != nil {
		return ResolveOptions{}, err
	}

	userConfigPath, err = existingConfigFile(userConfigPath)
	if err != nil {
		return ResolveOptions{}, err
	}

	projectConfigPath, err := existingConfigFile(filepath.Join(workspaceRoot, ".zero", "config.json"))
	if err != nil {
		return ResolveOptions{}, err
	}

	return ResolveOptions{
		UserConfigPath:    userConfigPath,
		ProjectConfigPath: projectConfigPath,
		ProviderCommand:   strings.TrimSpace(os.Getenv(ProviderCommandEnv)),
	}, nil
}

func DefaultUserConfigPath() (string, error) {
	userConfigDir, err := UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(userConfigDir, "zero", "config.json"), nil
}

// UserConfigDir returns the base directory Zero stores per-user config under.
// It mirrors os.UserConfigDir everywhere except macOS: there Go defaults to
// ~/Library/Application Support, but Zero deliberately uses ~/.config (XDG-style,
// matching Linux and Claude Code) so a single config path works cross-platform.
// $XDG_CONFIG_HOME still wins on macOS when set.
func UserConfigDir() (string, error) {
	if runtime.GOOS == "darwin" {
		if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
			return xdg, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, ".config"), nil
	}
	return os.UserConfigDir()
}

func existingConfigFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("inspect config %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("config path %s is a directory, want a file", path)
	}
	return path, nil
}
