package modelregistry

import "sync"

// resetModelsDevCacheForTest clears the process-level cache memoization and
// disables the overlay.
func resetModelsDevCacheForTest() {
	modelsDevOnce = sync.Once{}
	modelsDevCached = nil
	modelsDevEnabled.Store(false)
}

// Modes returns a copy of the preset catalog, preserving declaration order so
// list output and help text stay stable.
func Modes() []Mode {
	modes := make([]Mode, len(defaultModes))
	for index, mode := range defaultModes {
		modes[index] = cloneMode(mode)
	}
	return modes
}
