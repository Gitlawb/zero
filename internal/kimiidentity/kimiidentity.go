// Package kimiidentity builds the X-Msh-* vendor-identity headers Kimi
// Code's backend requires on every request — OAuth device authorization,
// token polling, refresh, AND managed-endpoint model calls. It exists as its
// own dependency-free package because both internal/oauth (login/refresh)
// and internal/providercatalog (the kimi-code descriptor's CustomHeaders,
// applied to runtime completions) must send the SAME identity: a login
// accepted under one device identity and completions sent under another (or
// under none) is rejected by the backend.
//
// Header names and general shape are reverse-engineered from the
// open-source kimi-cli client (src/kimi_cli/auth/oauth.py, _common_headers);
// Kimi has no published public API documentation for this, so these values
// are a best-effort match, not a verified spec.
package kimiidentity

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Headers returns the X-Msh-* vendor-identity headers, including the stable
// per-device identifier.
func Headers() map[string]string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unknown-host"
	}
	return map[string]string{
		"X-Msh-Platform":     "zero-cli",
		"X-Msh-Version":      "unknown",
		"X-Msh-Device-Name":  asciiHeaderValue(hostname),
		"X-Msh-Device-Model": asciiHeaderValue(runtime.GOOS + " " + runtime.GOARCH),
		"X-Msh-Os-Version":   runtime.GOOS,
		"X-Msh-Device-Id":    DeviceID(),
	}
}

// DeviceID returns the persistent device identifier sent as X-Msh-Device-Id.
// Kimi Code's own CLI persists this to ~/.kimi/device_id so the same value
// follows a device across logins, refreshes, and model calls; mirroring
// that, the ID is stored under the user config dir (zero/kimi-device-id) and
// minted once on first use. When the config dir is unavailable the ID is
// still stable for the life of the process.
var DeviceID = sync.OnceValue(loadOrCreateDeviceID)

func loadOrCreateDeviceID() string {
	path := deviceIDPath()
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			if id := strings.TrimSpace(string(raw)); isUUID(id) {
				return id
			}
		}
	}
	id := generateDeviceID()
	if path == "" {
		return id
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return id
	}
	// Create exclusively rather than os.WriteFile: two processes racing on
	// first use must converge on the SAME id, since a login accepted under
	// one device identity and completions sent under another are rejected
	// by the backend. The loser reads back the winner's file instead of
	// overwriting it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			if raw, readErr := os.ReadFile(path); readErr == nil {
				if existingID := strings.TrimSpace(string(raw)); isUUID(existingID) {
					return existingID
				}
			}
		}
		return id
	}
	defer f.Close()
	_, _ = f.WriteString(id + "\n")
	return id
}

func deviceIDPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		return ""
	}
	return filepath.Join(configDir, "zero", "kimi-device-id")
}

func generateDeviceID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "00000000-0000-0000-0000-000000000000"
	}
	raw[6] = (raw[6] & 0x0f) | 0x40 // version 4
	raw[8] = (raw[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

// asciiHeaderValue strips anything outside printable ASCII (0x20-0x7e). This
// mirrors a defensive fix kimi-cli itself needed: a raw platform-version
// string containing "#" broke an HTTP client's header validation on Linux
// (MoonshotAI/kimi-cli#1169) because HTTP header values must not contain
// control characters.
func asciiHeaderValue(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r <= 0x7e {
			b.WriteRune(r)
		}
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" {
		return "unknown"
	}
	return clean
}
