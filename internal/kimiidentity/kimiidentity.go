// Package kimiidentity builds the X-Msh-* vendor-identity headers Kimi
// Code's backend requires on every request — OAuth device authorization,
// token polling, refresh, AND managed-endpoint model calls. It is shared by
// both internal/oauth (login/refresh) and internal/providercatalog (the
// kimi-code descriptor's CustomHeaders, applied to runtime completions) so
// they send the SAME identity: a login accepted under one device identity
// and completions sent under another (or under none) is rejected by the
// backend.
//
// Header names and general shape are reverse-engineered from the
// open-source kimi-cli client (src/kimi_cli/auth/oauth.py, _common_headers);
// Kimi has no published public API documentation for this, so these values
// are a best-effort match, not a verified spec.
package kimiidentity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/lockutil"
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
	cachedDevicePath string
	cachedDeviceID   string
)

// DeviceID returns the persistent device identifier sent as X-Msh-Device-Id.
// Kimi Code's own CLI persists this to ~/.kimi/device_id so the same value
// follows a device across logins, refreshes, and model calls; mirroring
// that, the ID is stored under the user config dir (zero/kimi-device-id) and
// minted once on first use. Acquisition is bounded: an existing valid ID is
// returned, a live publisher is waited on only up to deviceIDMaxWait, a
// proven-dead or expired lease is reclaimed, and unreadable/unwritable
// storage, cancellation, or a live holder that outlasts the wait all return
// a process-local ID without overwriting a file this process does not own.
//
// The cache is keyed by the resolved storage path so tests that redirect
// os.UserConfigDir (via XDG_CONFIG_HOME / APPDATA / HOME) pick up a fresh
// identity without a separate test-only reset hook.
func DeviceID() string {
	deviceIDMu.Lock()
	defer deviceIDMu.Unlock()
	path := deviceIDPath()
	if cachedDeviceID != "" && cachedDevicePath == path {
		return cachedDeviceID
	}
	id := loadOrCreateDeviceIDAt(path)
	cachedDevicePath = path
	cachedDeviceID = id
	return id
}

// loadOrCreateDeviceIDAt is the real load-or-create logic behind DeviceID,
// parameterized by the storage path so tests can exercise production code
// directly (env var indirection through os.UserConfigDir is not portable to
// redirect in tests). It reads an existing UUID if present, otherwise mints
// one and persists it exclusively (see the concurrency note below).
//
// path must be of the form <configRoot>/zero/kimi-device-id. All file
// operations bind to an opened <configRoot> handle and then the zero/
// subdirectory so a symlink at zero cannot redirect device-id, lock, or
// temporary-file traffic outside the configuration root.
func loadOrCreateDeviceIDAt(path string) string {
	if path == "" {
		return generateDeviceID()
	}
	root, name, err := openDeviceIDDir(path)
	if err != nil {
		return generateDeviceID()
	}
	defer root.Close()
	if id := readValidDeviceID(root, name); id != "" {
		return id
	}
	return publishOrAdoptDeviceID(context.Background(), root, name, generateDeviceID())
}

// loadOrCreateDeviceIDAtContext is loadOrCreateDeviceIDAt with a caller
// context. Cancellation returns a process-local id without touching a live
// holder's persisted file.
func loadOrCreateDeviceIDAtContext(ctx context.Context, path string) string {
	if path == "" {
		return generateDeviceID()
	}
	root, name, err := openDeviceIDDir(path)
	if err != nil {
		return generateDeviceID()
	}
	defer root.Close()
	if id := readValidDeviceID(root, name); id != "" {
		return id
	}
	return publishOrAdoptDeviceID(ctx, root, name, generateDeviceID())
}

// openDeviceIDDir opens the zero/ directory under the configuration root for
// path (<configRoot>/zero/<name>) using rooted, traversal-resistant handles.
// A zero component that is a symlink escaping the config root is rejected by
// Root.OpenRoot rather than followed into attacker-controlled storage.
func openDeviceIDDir(path string) (*os.Root, string, error) {
	name := filepath.Base(path)
	zeroDir := filepath.Dir(path)
	configDir := filepath.Dir(zeroDir)
	zeroName := filepath.Base(zeroDir)
	if name == "" || name == "." || zeroName == "" || zeroName == "." || configDir == "" || configDir == "." {
		return nil, "", fmt.Errorf("kimiidentity: invalid device-id path %q", path)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, "", err
	}
	cfgRoot, err := os.OpenRoot(configDir)
	if err != nil {
		return nil, "", err
	}
	// Best-effort create; Exist is fine. Root refuses a zero symlink that
	// points outside configDir on the subsequent OpenRoot.
	_ = cfgRoot.Mkdir(zeroName, 0o700)
	zeroRoot, err := cfgRoot.OpenRoot(zeroName)
	_ = cfgRoot.Close()
	if err != nil {
		return nil, "", err
	}
	return zeroRoot, name, nil
}

var (
	beforeRenameHook func()
	readDeviceLock   = func(root *os.Root, name string) ([]byte, error) {
		return root.ReadFile(name)
	}
	deviceIDNow      = time.Now
	deviceIDMaxWait  = 5 * time.Second
	deviceIDLeaseTTL = 10 * time.Second
)

type publishOutcome int

const (
	publishOK publishOutcome = iota
	publishContended
	publishPersistFailed
)

func publishDeviceIDAsHolder(root *os.Root, name, lockName, id, ownerToken string) (string, publishOutcome) {
	tmpLockName := fmt.Sprintf("%s.tmp.%s", lockName, ownerToken)
	if err := root.WriteFile(tmpLockName, []byte(ownerToken+"\n"), 0o600); err != nil {
		return "", publishPersistFailed
	}
	defer func() { _ = root.Remove(tmpLockName) }()

	if err := root.Link(tmpLockName, lockName); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", publishContended
		}
		// Fallback for filesystems where Link is unsupported:
		lock, oerr := root.OpenFile(lockName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if oerr != nil {
			if errors.Is(oerr, os.ErrExist) {
				return "", publishContended
			}
			return "", publishPersistFailed
		}
		if _, werr := lock.WriteString(ownerToken + "\n"); werr != nil {
			_ = lock.Close()
			_ = root.Remove(lockName)
			return "", publishPersistFailed
		}
		if serr := lock.Sync(); serr != nil {
			_ = lock.Close()
			_ = root.Remove(lockName)
			return "", publishPersistFailed
		}
		_ = lock.Close()
	}

	defer func() {
		if curRaw, rerr := root.ReadFile(lockName); rerr == nil && strings.TrimSpace(string(curRaw)) == ownerToken {
			_ = root.Remove(lockName)
		}
	}()

	if existingID := readValidDeviceID(root, name); existingID != "" {
		return existingID, publishOK
	}

	tmpName := tmpDeviceIDName(name)
	if err := writeDeviceIDFile(root, tmpName, id); err != nil {
		if existingID := readValidDeviceIDWithRetry(root, name); existingID != "" {
			return existingID, publishOK
		}
		return "", publishPersistFailed
	}
	defer func() { _ = root.Remove(tmpName) }()

	if beforeRenameHook != nil {
		beforeRenameHook()
	}

	if err := root.Rename(tmpName, name); err != nil {
		if existingID := readValidDeviceIDWithRetry(root, name); existingID != "" {
			return existingID, publishOK
		}
		return "", publishPersistFailed
	}
	if existingID := readValidDeviceID(root, name); existingID != "" {
		return existingID, publishOK
	}
	return id, publishOK
}

func publishOrAdoptDeviceID(ctx context.Context, root *os.Root, name, id string) string {
	if existingID := readValidDeviceID(root, name); existingID != "" {
		return existingID
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lockName := name + ".lock"
	ownerToken := fmt.Sprintf("%d.%d", os.Getpid(), deviceIDNow().UnixNano())
	deadline := deviceIDNow().Add(deviceIDMaxWait)

	const pollInterval = 10 * time.Millisecond

	for {
		if existingID := readValidDeviceID(root, name); existingID != "" {
			return existingID
		}
		if ctx.Err() != nil {
			return id
		}

		publishedID, outcome := publishDeviceIDAsHolder(root, name, lockName, id, ownerToken)
		switch outcome {
		case publishOK:
			return publishedID
		case publishPersistFailed:
			if existingID := readValidDeviceID(root, name); existingID != "" {
				return existingID
			}
			return id
		}

		if existingID := readValidDeviceID(root, name); existingID != "" {
			return existingID
		}

		raw, rerr := readDeviceLock(root, lockName)
		switch {
		case rerr != nil && !errors.Is(rerr, os.ErrNotExist):
			// Windows often returns sharing-violation or access-denied
			// while the holder is deleting the lock after publish. That is
			// contention, not a dead store: returning a process-local id
			// here makes concurrent first-use callers diverge.
			if existingID := readValidDeviceID(root, name); existingID != "" {
				return existingID
			}
			if !deviceIDNow().Before(deadline) || ctx.Err() != nil {
				return id
			}
			time.Sleep(pollInterval)
			continue
		case rerr == nil && lockHolderAlive(raw):
			if !deviceIDNow().Before(deadline) || ctx.Err() != nil {
				if existingID := readValidDeviceID(root, name); existingID != "" {
					return existingID
				}
				return id
			}
			time.Sleep(pollInterval)
			continue
		}

		reclaimed, rerr := reclaimDeadRepairLock(root, lockName)
		if rerr != nil {
			return id
		}
		if reclaimed {
			continue
		}
		if !deviceIDNow().Before(deadline) || ctx.Err() != nil {
			return id
		}
		time.Sleep(pollInterval)
	}
}

// reclaimDeadRepairLock renames the repair lock aside and keeps it only when
// the holder is proven dead (or the lock is empty/corrupt and therefore not a
// live lease). Uses lockutil's rooted reclaim so only one racer wins the
// rename-aside, a live holder's lock is restored rather than stolen, and every
// rename/read/remove stays inside the opened root handle instead of re-walking
// root.Name()+lockName as plain paths (a symlink or reparse point swapped in
// under the lock name after the root was opened cannot redirect them).
func reclaimDeadRepairLock(root *os.Root, lockName string) (bool, error) {
	suffix := fmt.Sprintf("%d.%d", os.Getpid(), time.Now().UnixNano())
	return lockutil.ReclaimStaleLockRooted(root, lockName, suffix, lockHolderAlive)
}

// lockHolderAlive reports whether the repair-lock contents still represent a
// live holder. Token format is "<pid>.<nano>". Empty or unparseable contents
// are treated as dead (abandoned claim) so a crashed mid-write holder can be
// recovered. A parseable live PID is not enough: the lease also expires after
// deviceIDLeaseTTL so a reused PID cannot pin the lock forever.
func lockHolderAlive(raw []byte) bool {
	pid, issued, ok := parseLockToken(strings.TrimSpace(string(raw)))
	if !ok || pid <= 0 {
		return false
	}
	if deviceIDNow().Sub(issued) > deviceIDLeaseTTL {
		return false
	}
	return processAlive(pid)
}

func parseLockToken(token string) (pid int, issued time.Time, ok bool) {
	if token == "" {
		return 0, time.Time{}, false
	}
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return 0, time.Time{}, false
	}
	pid, err := strconv.Atoi(token[:dot])
	if err != nil {
		return 0, time.Time{}, false
	}
	nano, err := strconv.ParseInt(token[dot+1:], 10, 64)
	if err != nil || nano <= 0 {
		return 0, time.Time{}, false
	}
	return pid, time.Unix(0, nano), true
}

// writeDeviceIDFile writes a complete id+"\n" to root/name, checking write,
// sync, and close errors. On any failure the partial file is removed.
func writeDeviceIDFile(root *os.Root, name, id string) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(id + "\n"); err != nil {
		_ = f.Close()
		_ = root.Remove(name)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = root.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(name)
		return err
	}
	return nil
}

func tmpDeviceIDName(name string) string {
	return fmt.Sprintf("%s.tmp.%d.%d", name, os.Getpid(), time.Now().UnixNano())
}

// readValidDeviceID returns a UUID from root/name, or "" if missing/invalid.
func readValidDeviceID(root *os.Root, name string) string {
	raw, err := root.ReadFile(name)
	if err != nil {
		return ""
	}
	if id := strings.TrimSpace(string(raw)); isUUID(id) {
		return id
	}
	return ""
}

// readValidDeviceIDWithRetry re-reads briefly so a process that lost the
// exclusive create can adopt the winner even if it observed the file before
// the winner finished publishing the UUID.
func readValidDeviceIDWithRetry(root *os.Root, name string) string {
	const attempts = 40
	const delay = 5 * time.Millisecond
	for i := 0; i < attempts; i++ {
		if id := readValidDeviceID(root, name); id != "" {
			return id
		}
		time.Sleep(delay)
	}
	return ""
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
