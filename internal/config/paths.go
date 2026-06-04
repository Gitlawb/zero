package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultResolveOptions builds config resolution inputs from the local process
// environment and workspace.
func DefaultResolveOptions(workspaceRoot string) (ResolveOptions, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return ResolveOptions{}, fmt.Errorf("resolve user config directory: %w", err)
	}

	userConfigPath, err := existingConfigFile(filepath.Join(userConfigDir, "zero", "config.json"))
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
		ProviderCommand:   strings.TrimSpace(os.Getenv("ZERO_PROVIDER_COMMAND")),
	}, nil
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
