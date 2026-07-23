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
	"time"
)

// Headers returns the X-Msh-* vendor-identity headers, including the stable
// per-device identifier.
//
// X-Msh-Platform is "kimi_code_cli". That is the value Moonshot's own Kimi
// Code CLI sends (packages/oauth/src/identity.ts, KIMI_CODE_PLATFORM) as of
// its oauth package changelog entry correcting the header from an earlier
// "kimi-code-cli" typo (PR MoonshotAI/kimi-code#52, commit 064343a); the
// older, separate open-source kimi-cli client instead hardcodes "kimi_cli".
// Kimi's coding/v1 endpoint documents a client whitelist ("Kimi CLI, Claude
// Code, Roo Code, ..."); sending the wrong platform value risks the managed
// endpoint rejecting completions even after a successful login.
func Headers() map[string]string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unknown-host"
	}
	return map[string]string{
		"X-Msh-Platform":     "kimi_code_cli",
		"X-Msh-Version":      "unknown",
		"X-Msh-Device-Name":  asciiHeaderValue(hostname),
		"X-Msh-Device-Model": asciiHeaderValue(runtime.GOOS + " " + runtime.GOARCH),
		"X-Msh-Os-Version":   runtime.GOOS,
		"X-Msh-Device-Id":    DeviceID(),
	}
}

var (
	deviceIDMu       sync.Mutex
	testDeviceIDPath string
	cachedDeviceID   = sync.OnceValue(func() string { return loadOrCreateDeviceIDAt(deviceIDPath()) })
)

// DeviceID returns the persistent device identifier sent as X-Msh-Device-Id.
// Kimi Code's own CLI persists this to ~/.kimi/device_id so the same value
// follows a device across logins, refreshes, and model calls; mirroring
// that, the ID is stored under the user config dir (zero/kimi-device-id) and
// minted once on first use. When the config dir is unavailable the ID is
// still stable for the life of the process.
func DeviceID() string {
	deviceIDMu.Lock()
	fn := cachedDeviceID
	deviceIDMu.Unlock()
	return fn()
}

// ResetDeviceIDForTest resets the process-global DeviceID cache so unit tests
// can inspect or isolate device identity creation without state pollution.
func ResetDeviceIDForTest() {
	deviceIDMu.Lock()
	cachedDeviceID = sync.OnceValue(func() string { return loadOrCreateDeviceIDAt(deviceIDPath()) })
	deviceIDMu.Unlock()
}

// SetDeviceIDPathForTest configures an explicit storage path for device ID in
// unit tests, overriding os.UserConfigDir(). It resets the DeviceID cache and
// returns a cleanup function that restores the original path and resets the
// cache.
func SetDeviceIDPathForTest(path string) func() {
	deviceIDMu.Lock()
	prevPath := testDeviceIDPath
	testDeviceIDPath = path
	cachedDeviceID = sync.OnceValue(func() string { return loadOrCreateDeviceIDAt(deviceIDPath()) })
	deviceIDMu.Unlock()
	return func() {
		deviceIDMu.Lock()
		testDeviceIDPath = prevPath
		cachedDeviceID = sync.OnceValue(func() string { return loadOrCreateDeviceIDAt(deviceIDPath()) })
		deviceIDMu.Unlock()
	}
}

// loadOrCreateDeviceIDAt is the real load-or-create logic behind DeviceID,
// parameterized by the storage path so tests can exercise production code
// directly (env var indirection through os.UserConfigDir is not portable to
// redirect in tests). It reads an existing UUID if present, otherwise mints
// one and persists it exclusively (see the concurrency note below).
func loadOrCreateDeviceIDAt(path string) string {
	if path != "" {
		if id := readValidDeviceID(path); id != "" {
			return id
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
	// overwriting it. After O_EXCL succeeds the winner still has to write
	// content, so a concurrent loser may briefly observe an empty file;
	// readValidDeviceIDWithRetry waits for the UUID rather than minting a
	// second divergent identity. If the winner dies mid-publish leaving an
	// empty/invalid file, the next caller removes that abandoned file once
	// and retries exclusive create so the identity is repaired instead of
	// permanently stuck on a divergent in-memory id.
	return createOrAdoptDeviceID(path, id)
}

// createOrAdoptDeviceID publishes id at path or adopts a concurrent winner.
func createOrAdoptDeviceID(path, id string) string {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return id
		}
		if existingID := readValidDeviceIDWithRetry(path); existingID != "" {
			return existingID
		}
		// path exists but never became a valid UUID (abandoned create,
		// corrupt file). Repair it rather than removing it ourselves: an
		// unlocked remove here could unlink another racer's just-published
		// winner between our failed read and the remove call, handing that
		// racer back an id that is no longer the one on disk.
		return repairAbandonedDeviceID(path, id)
	}
	_, _ = f.WriteString(id + "\n")
	_ = f.Sync()
	_ = f.Close()
	// Re-read so a racing repair that replaced us still converges.
	if existingID := readValidDeviceID(path); existingID != "" {
		return existingID
	}
	return id
}

// repairAbandonedDeviceID fixes an invalid/empty device-id file left behind
// by a process that exclusive-created path and died before writing a UUID.
// Repair itself is serialized through an exclusive lock file so only one
// racing process ever removes and recreates path: without that, one process
// could unlink another's freshly published replacement and mint a second,
// divergent id, leaving the first process holding an id that stops matching
// what is actually persisted. Callers that lose the lock wait for the holder
// to publish instead of attempting their own repair. If the lock holder dies
// mid-repair without publishing or unlocking, the lock is broken and retried.
func repairAbandonedDeviceID(path, id string) string {
	if existingID := readValidDeviceID(path); existingID != "" {
		return existingID
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return id
		}
		if existingID := readValidDeviceIDWithRetry(path); existingID != "" {
			return existingID
		}
		// If lockPath exists but is stale (owner crashed), break lock and retry once.
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 2*time.Second {
			_ = os.Remove(lockPath)
			lock, err = os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		}
		if err != nil {
			if existingID := readValidDeviceIDWithRetry(path); existingID != "" {
				return existingID
			}
			return id
		}
	}
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()

	if existingID := readValidDeviceID(path); existingID != "" {
		return existingID
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, []byte(id+"\n"), 0o600); err != nil {
		return id
	}
	defer func() { _ = os.Remove(tmpPath) }()

	_ = os.Remove(path)
	if err := os.Rename(tmpPath, path); err != nil {
		if errWrite := os.WriteFile(path, []byte(id+"\n"), 0o600); errWrite == nil {
			return id
		}
		if existingID := readValidDeviceIDWithRetry(path); existingID != "" {
			return existingID
		}
		return id
	}
	return id
}

// readValidDeviceID returns a UUID from path, or "" if missing/invalid.
func readValidDeviceID(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if id := strings.TrimSpace(string(raw)); isUUID(id) {
		return id
	}
	return ""
}

// readValidDeviceIDWithRetry re-reads path briefly so a process that lost the
// exclusive create can adopt the winner even if it observed the file before
// the winner finished writing the UUID.
func readValidDeviceIDWithRetry(path string) string {
	const attempts = 40
	const delay = 5 * time.Millisecond
	for i := 0; i < attempts; i++ {
		if id := readValidDeviceID(path); id != "" {
			return id
		}
		time.Sleep(delay)
	}
	return ""
}

func deviceIDPath() string {
	deviceIDMu.Lock()
	testPath := testDeviceIDPath
	deviceIDMu.Unlock()
	if testPath != "" {
		return testPath
	}
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
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
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
