package plugins

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotInstalled is returned when Info cannot find a plugin with the requested id.
var ErrNotInstalled = errors.New("plugin is not installed")

// PluginInfo combines a loaded plugin with optional lockfile metadata.
type PluginInfo struct {
	Plugin     LoadedPlugin `json:"plugin"`
	LockSource string       `json:"lockSource,omitempty"`
	LockHash   string       `json:"lockHash,omitempty"`
	HashDrift  bool         `json:"hashDrift,omitempty"`
}

// InfoOptions controls plugin lookup and lockfile resolution for Info.
type InfoOptions struct {
	LoadOptions LoadOptions
	LockDir     string
}

// Info returns details for the named plugin after normal discovery precedence.
func Info(options InfoOptions, id string) (PluginInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return PluginInfo{}, errors.New("plugin id is required")
	}
	result, err := Load(options.LoadOptions)
	if err != nil {
		return PluginInfo{}, err
	}
	var plugin LoadedPlugin
	found := false
	for _, candidate := range result.Plugins {
		if candidate.ID == id {
			plugin = candidate
			found = true
			break
		}
	}
	if !found {
		return PluginInfo{}, fmt.Errorf("%w: %q", ErrNotInstalled, id)
	}

	info := PluginInfo{Plugin: plugin}
	lockDir := strings.TrimSpace(options.LockDir)
	if lockDir != "" {
		if lock, readErr := ReadLock(lockDir); readErr == nil {
			if entry, ok := lock[plugin.ID]; ok {
				info.LockSource = entry.Source
				info.LockHash = entry.Hash
			}
		}
	}
	if info.LockHash != "" {
		if current, hashErr := hashTree(plugin.PluginDir); hashErr == nil {
			info.HashDrift = current != info.LockHash
		}
	}
	return info, nil
}
