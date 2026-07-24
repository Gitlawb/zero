package update

import "testing"

// stubRandomStagingSuffix overrides randomStagingSuffix to always return a
// fixed value for the duration of t, restoring the original on cleanup.
// stagingFilePath's random suffix is unpredictable by design in production,
// so tests that need to know (or pre-occupy) the exact staging path pin it
// here instead.
func stubRandomStagingSuffix(t *testing.T, suffix string) {
	t.Helper()
	original := randomStagingSuffix
	randomStagingSuffix = func() (string, error) { return suffix, nil }
	t.Cleanup(func() { randomStagingSuffix = original })
}
