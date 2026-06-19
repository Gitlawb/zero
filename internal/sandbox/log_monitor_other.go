//go:build !darwin

package sandbox

import "context"

// DenialMonitor is a no-op outside macOS: the `log stream` seatbelt-denial feed is
// macOS-specific, and the bubblewrap backend surfaces failures differently.
type DenialMonitor struct{}

// StartDenialMonitor returns a no-op monitor on non-macOS platforms.
func StartDenialMonitor(_ context.Context, _ string) *DenialMonitor { return &DenialMonitor{} }

// Stop returns no blocks on non-macOS platforms.
func (monitor *DenialMonitor) Stop() []string { return nil }
