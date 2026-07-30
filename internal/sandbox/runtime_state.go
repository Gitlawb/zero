package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var sandboxUserCacheDir = os.UserCacheDir
var sandboxRuntimeNow = time.Now

const (
	sandboxRuntimeMaxAge   = 30 * 24 * time.Hour
	sandboxRuntimeMaxRoots = 64
)

var fallbackSandboxRuntimes = struct {
	sync.Mutex
	roots map[string]string
}{roots: make(map[string]string)}

type SandboxRuntime struct {
	Root  string `json:"root,omitempty"`
	Cache string `json:"cache,omitempty"`
	Data  string `json:"data,omitempty"`
	Temp  string `json:"temp,omitempty"`
}

// sandboxRuntimeRootFor derives the per-workspace runtime root. It is separated
// from prepareSandboxRuntime because the elevated Windows setup path needs the
// same answer WITHOUT taking a lease or creating anything: a sandbox principal
// is a separate account with no inherited rights under the user cache, so setup
// has to grant it write access to this tree before any command runs.
//
// Both callers must agree exactly. If they ever drift, setup grants the ACE on
// one directory while commands write to another, and the failure is a bare
// ACCESS_DENIED from npm or go build with nothing pointing at the sandbox.
func sandboxRuntimeRootFor(workspaceRoot string, cacheRoot string) (string, error) {
	digest := sha256.Sum256([]byte(workspaceRoot))
	root := filepath.Join(cacheRoot, "zero", "runtime", "v1", hex.EncodeToString(digest[:8]))
	if !pathWithinRoot(workspaceRoot, root) {
		return root, nil
	}
	return fallbackSandboxRuntimeRoot(workspaceRoot)
}

func prepareSandboxRuntime(workspaceRoot string) (SandboxRuntime, func(), error) {
	workspaceRoot = canonicalSandboxWorkspaceRoot(workspaceRoot)
	if workspaceRoot == "" || workspaceRoot == "." {
		return SandboxRuntime{}, nil, errors.New("sandbox runtime requires a workspace root")
	}
	cacheRoot, err := sandboxUserCacheDir()
	if err != nil {
		return SandboxRuntime{}, nil, fmt.Errorf("resolve user cache directory: %w", err)
	}
	// Canonicalized the SAME way as the workspace root, because
	// sandboxRuntimeRootFor compares the two: it falls back to a private temp
	// tree when the derived runtime root would land inside the workspace.
	// Normalizing only one side made that comparison run on two different
	// spellings of the same path — /var vs /private/var on macOS, an 8.3 short
	// name vs its long form on Windows — so the containment check missed and the
	// fallback never fired.
	cacheRoot = canonicalSandboxWorkspaceRoot(cacheRoot)
	if cacheRoot == "" || cacheRoot == "." {
		return SandboxRuntime{}, nil, errors.New("user cache directory is unavailable")
	}
	root, err := sandboxRuntimeRootFor(workspaceRoot, cacheRoot)
	if err != nil {
		return SandboxRuntime{}, nil, err
	}
	lease, err := prepareSandboxRuntimeLease(root)
	if err != nil {
		root, err = fallbackSandboxRuntimeRoot(workspaceRoot)
		if err != nil {
			return SandboxRuntime{}, nil, err
		}
		lease, err = prepareSandboxRuntimeLease(root)
		if err != nil {
			return SandboxRuntime{}, nil, err
		}
	}
	prepared := false
	defer func() {
		if !prepared {
			lease.release()
		}
	}()
	runtimeState := SandboxRuntime{
		Root:  root,
		Cache: filepath.Join(root, "cache"),
		Data:  filepath.Join(root, "data"),
		Temp:  filepath.Join(root, "tmp"),
	}
	directories := []string{
		runtimeState.Root,
		runtimeState.Cache,
		runtimeState.Data,
		runtimeState.Temp,
		filepath.Join(runtimeState.Cache, "npm"),
		filepath.Join(runtimeState.Cache, "yarn"),
		filepath.Join(runtimeState.Cache, "corepack"),
		filepath.Join(runtimeState.Cache, "pip"),
		filepath.Join(runtimeState.Cache, "go-build"),
		filepath.Join(runtimeState.Data, "go-mod"),
		filepath.Join(runtimeState.Data, "cargo"),
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return SandboxRuntime{}, nil, fmt.Errorf("create sandbox runtime directory %s: %w", directory, err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return SandboxRuntime{}, nil, fmt.Errorf("secure sandbox runtime directory %s: %w", directory, err)
		}
	}
	now := sandboxRuntimeNow()
	if err := os.Chtimes(runtimeState.Root, now, now); err != nil {
		return SandboxRuntime{}, nil, fmt.Errorf("touch sandbox runtime root: %w", err)
	}
	cleanupSandboxRuntimeRoots(filepath.Dir(runtimeState.Root), runtimeState.Root, now)
	prepared = true
	return runtimeState, lease.release, nil
}

func prepareSandboxRuntimeLease(root string) (*sandboxRuntimeLease, error) {
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return nil, fmt.Errorf("create sandbox runtime parent: %w", err)
	}
	return acquireSandboxRuntimeLease(root)
}

// cleanupSandboxRuntimeRoots applies a conservative age/count policy. Cleanup
// is best-effort and never removes the runtime selected for the current
// command, so cache maintenance cannot turn a valid execution into a failure.
func cleanupSandboxRuntimeRoots(parent, current string, now time.Time) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		path := filepath.Join(parent, entry.Name())
		if !entry.IsDir() || filepath.Clean(path) == filepath.Clean(current) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if now.Sub(info.ModTime()) > sandboxRuntimeMaxAge {
			removeSandboxRuntimeRootIfUnused(path)
			continue
		}
		candidates = append(candidates, candidate{path: path, modTime: info.ModTime()})
	}
	if len(candidates) < sandboxRuntimeMaxRoots {
		return
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modTime.Before(candidates[j].modTime) })
	for len(candidates) >= sandboxRuntimeMaxRoots {
		removeSandboxRuntimeRootIfUnused(candidates[0].path)
		candidates = candidates[1:]
	}
}

func removeSandboxRuntimeRootIfUnused(root string) {
	lease, inUse, err := tryAcquireSandboxRuntimeCleanupLease(root)
	if err != nil || inUse {
		return
	}
	defer lease.release()
	_ = os.RemoveAll(root)
}

func combineSandboxCleanups(cleanups ...func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, cleanup := range cleanups {
				if cleanup != nil {
					cleanup()
				}
			}
		})
	}
}

func fallbackSandboxRuntimeRoot(workspaceRoot string) (string, error) {
	fallbackSandboxRuntimes.Lock()
	defer fallbackSandboxRuntimes.Unlock()
	if root := fallbackSandboxRuntimes.roots[workspaceRoot]; root != "" {
		return root, nil
	}
	parent, err := os.MkdirTemp("", "zero-runtime-")
	if err != nil {
		return "", fmt.Errorf("create fallback sandbox runtime: %w", err)
	}
	root := filepath.Join(parent, "runtime")
	if pathWithinRoot(workspaceRoot, root) {
		_ = os.RemoveAll(parent)
		return "", fmt.Errorf("fallback sandbox runtime root %q must be outside workspace %q", root, workspaceRoot)
	}
	fallbackSandboxRuntimes.roots[workspaceRoot] = root
	return root, nil
}

func sandboxRuntimeEnvironment(env []string, runtimeState *SandboxRuntime) []string {
	if runtimeState == nil || strings.TrimSpace(runtimeState.Root) == "" {
		return env
	}
	overrides := []string{
		"XDG_CACHE_HOME=" + runtimeState.Cache,
		"TMPDIR=" + runtimeState.Temp,
		"TMP=" + runtimeState.Temp,
		"TEMP=" + runtimeState.Temp,
		"npm_config_cache=" + filepath.Join(runtimeState.Cache, "npm"),
		"YARN_CACHE_FOLDER=" + filepath.Join(runtimeState.Cache, "yarn"),
		"COREPACK_HOME=" + filepath.Join(runtimeState.Cache, "corepack"),
		"PIP_CACHE_DIR=" + filepath.Join(runtimeState.Cache, "pip"),
		"GOCACHE=" + filepath.Join(runtimeState.Cache, "go-build"),
		"GOMODCACHE=" + filepath.Join(runtimeState.Data, "go-mod"),
		"CARGO_HOME=" + filepath.Join(runtimeState.Data, "cargo"),
	}
	return upsertEnvList(env, overrides...)
}

func permissionProfileWithRuntime(profile PermissionProfile, runtimeState SandboxRuntime) PermissionProfile {
	profile.Runtime = &runtimeState
	if profile.FileSystem.Kind != FileSystemRestricted || runtimeState.Root == "" {
		return profile
	}
	for _, root := range profile.FileSystem.WriteRoots {
		if filepath.Clean(root.Root) == filepath.Clean(runtimeState.Root) {
			return profile
		}
	}
	profile.FileSystem.WriteRoots = append(profile.FileSystem.WriteRoots, WritableRoot{Root: runtimeState.Root})
	return profile
}

// canonicalSandboxWorkspaceRoot normalizes a workspace root the way
// Engine.resolveCommandDir already does — clean, absolutize, then resolve
// symlinks — so every derivation keyed to a workspace agrees on the string.
//
// The runtime root is a hash of this, and the elevated Windows setup grants the
// principal that tree while commands derive it again. Cleaning alone was not
// enough for the two to agree, and it does not take a symlink for them to
// differ: a path opened in different casing, or through an 8.3 short name (what
// a Windows CI runner's TEMP looks like), resolves to a different spelling.
// Setup then granted one tree and every command used another, so the grant that
// makes npm/go/pip caches writable landed where nothing reads and surfaced as a
// bare ACCESS_DENIED.
//
// Resolution failing is not an error: an unresolvable root still needs a stable
// key, and falling back to the cleaned absolute path is what the command path
// does too.
func canonicalSandboxWorkspaceRoot(root string) string {
	cleaned := filepath.Clean(strings.TrimSpace(root))
	if cleaned == "" || cleaned == "." {
		return ""
	}
	if !filepath.IsAbs(cleaned) {
		if absolute, err := filepath.Abs(cleaned); err == nil {
			cleaned = absolute
		}
	}
	// EvalSymlinks fails outright when the LEAF does not exist, which is the
	// normal case for a cache or runtime root that has not been created yet. A
	// plain call therefore resolved an existing workspace while leaving a
	// not-yet-created cache root unresolved, and the two were compared against
	// each other — the containment check that decides whether the runtime tree
	// must move out of the workspace then ran on /private/var/... versus
	// /var/..., missed, and left the tree inside the workspace.
	//
	// Resolve the longest existing ancestor and re-append the rest, so a path
	// normalizes the same way whether or not its final segments exist yet.
	remainder := ""
	current := cleaned
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			if remainder == "" {
				return resolved
			}
			return filepath.Join(resolved, remainder)
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Nothing along the path resolved; the cleaned absolute form is the
			// best stable key available.
			return cleaned
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}
