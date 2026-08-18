package kimiidentity

import (
	"context"
	"os"
	"testing"
	"time"
)

// IsolateDeviceIDStorage redirects the env vars os.UserConfigDir consults so
// subsequent DeviceID/Headers calls store under root for the duration of t
// (and any nested tests that inherit the env). Sets XDG_CONFIG_HOME, APPDATA,
// and HOME together so the redirect is portable across Windows, macOS, and
// Linux without GOOS branching.
//
// DeviceID's cache is path-keyed, so no separate cache reset is required:
// once the config root changes, the next DeviceID/Headers call reloads.
//
// Cross-package tests cannot call this (export_test.go is package-local to
// go test of kimiidentity); they should set the same three env keys via
// t.Setenv on a t.TempDir() before invoking Headers/DeviceID or
// providercatalog.Get("kimi-code").
func IsolateDeviceIDStorage(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("HOME", root)
}

// SetBeforeRenameHook sets a hook invoked immediately before the staged
// device-id file is renamed into place. Returns a cleanup function.
func SetBeforeRenameHook(hook func()) func() {
	prev := beforeRenameHook
	beforeRenameHook = hook
	return func() {
		beforeRenameHook = prev
	}
}

func SetDeviceIDMaxWait(d time.Duration) func() {
	prev := deviceIDMaxWait
	deviceIDMaxWait = d
	return func() { deviceIDMaxWait = prev }
}

// SetReadDeviceLock replaces the lock-file reader. Tests use this to inject
// a transient Windows-style read failure while a holder is publishing.
func SetReadDeviceLock(fn func(root *os.Root, name string) ([]byte, error)) func() {
	prev := readDeviceLock
	if fn != nil {
		readDeviceLock = fn
	}
	return func() { readDeviceLock = prev }
}

func LoadOrCreateDeviceIDAtContext(ctx context.Context, path string) string {
	return loadOrCreateDeviceIDAtContext(ctx, path)
}
