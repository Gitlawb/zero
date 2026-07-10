package plugins

// Distribution: install a plugin from a git URL or a local path into a plugins
// directory, with the manifest validated and a content hash recorded in a
// lockfile (plugins.lock). Install copies the plugin tree verbatim but NEVER
// executes any of it — installed plugins still go through normal Stage 09
// activation with permission gating before any tool can run.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// manifestFileName is the plugin manifest filename, matching the loader.
	manifestFileName = "plugin.json"

	// LockFileName maps an installed plugin id to its source and content hash.
	LockFileName = "plugins.lock"

	disabledDirName       = ".disabled"
	pluginGitFetchTimeout = 2 * time.Minute
)

// ErrNameClash is returned when an install would overwrite a plugin already
// installed from a DIFFERENT source, unless InstallOptions.Force is set.
var ErrNameClash = errors.New("a different plugin with that id is already installed")

var installCommitPattern = regexp.MustCompile(`^[A-Fa-f0-9]{40}$`)

// GitRunner fetches the plugin at source into destination. The default runner
// shallow-clones with the system git (inheriting the process environment, so
// proxy/egress settings are honored). It is injectable so tests never hit the
// network. A runner must only fetch — it must never execute fetched content.
type GitRunner func(ctx context.Context, destination string, source string) error

// InstallOptions configures a single plugin install.
type InstallOptions struct {
	// Source is a git URL or a local filesystem path to a plugin directory (one
	// that contains a plugin.json, or whose tree contains exactly one).
	Source string
	// Dir is the plugins directory to install into (typically the user plugins
	// root from ResolveRoots).
	Dir string
	// Force allows overwriting a plugin installed from a different source.
	Force bool
	// GitRunner overrides the fetch implementation. When nil, a git source is
	// shallow-cloned with the system git.
	GitRunner GitRunner
	// Commit pins git installs to an exact commit. Local paths ignore this field
	// but still record it in the lockfile when supplied by a catalog release.
	Commit string
	// Subdir selects the plugin directory inside the fetched source.
	Subdir string
	// ExpectedHash, when set, must match the filtered tree hash before install.
	ExpectedHash string
	// ExpectedID/ExpectedVersion, when set, must match the parsed manifest.
	ExpectedID      string
	ExpectedVersion string
	// ExpectedComponents, when non-nil, must exactly match the parsed manifest's
	// tools/hooks/skills/prompts inventory by name plus permission/event where
	// applicable.
	ExpectedComponents *InstallComponentInventory
	// Catalog metadata is additive lockfile state used by marketplace installs.
	Catalog string
	Pinned  bool
}

type InstallComponentInventory struct {
	Tools   []InstallToolComponent
	Hooks   []InstallHookComponent
	Skills  []string
	Prompts []string
}

type InstallToolComponent struct {
	Name       string
	Permission ToolPermission
}

type InstallHookComponent struct {
	Name  string
	Event HookEvent
}

// InstallResult reports what an install did.
type InstallResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	ManifestPath string `json:"manifestPath"`
	Hash         string `json:"hash"`
	Source       string `json:"source"`
	Updated      bool   `json:"updated"`
	PreviousHash string `json:"previousHash,omitempty"`
}

type VerifyResult struct {
	ID           string `json:"id"`
	Hash         string `json:"hash"`
	ExpectedHash string `json:"expectedHash"`
	Enabled      bool   `json:"enabled"`
}

// LockEntry records the source and content hash for one installed plugin.
type LockEntry struct {
	Source  string `json:"source,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Catalog string `json:"catalog,omitempty"`
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Subdir  string `json:"subdir,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
	Pinned  bool   `json:"pinned,omitempty"`
}

// Install fetches the plugin at options.Source, validates its manifest, copies
// the plugin tree into options.Dir/<id>/, and records a content hash (over the
// manifest bytes) in the lockfile. Fetched content is never executed.
func Install(ctx context.Context, options InstallOptions) (InstallResult, error) {
	source := strings.TrimSpace(options.Source)
	if source == "" {
		return InstallResult{}, errors.New("a plugin source (git URL or path) is required")
	}
	dir := strings.TrimSpace(options.Dir)
	if dir == "" {
		return InstallResult{}, errors.New("a plugins directory is required")
	}
	// Canonicalize a local source so clash detection keys off the resolved path,
	// not the spelling the user typed (relative vs absolute, symlinked vs not).
	source = canonicalSource(source)
	options.Source = source
	options.Dir = dir

	var result InstallResult
	err := withPluginRootLock(dir, func() error {
		var installErr error
		result, installErr = installLocked(ctx, options)
		return installErr
	})
	return result, err
}

func installLocked(ctx context.Context, options InstallOptions) (InstallResult, error) {
	source := options.Source
	dir := options.Dir
	fetchDir, cleanup, err := fetchSource(ctx, source, options.GitRunner)
	if err != nil {
		return InstallResult{}, err
	}
	defer cleanup()
	if strings.TrimSpace(options.Commit) != "" && !isLocalPath(source) {
		if err := checkoutCommit(ctx, fetchDir, options.Commit); err != nil {
			return InstallResult{}, err
		}
	}

	sourceRoot := fetchDir
	if strings.TrimSpace(options.Subdir) != "" {
		sourceRoot = filepath.Join(fetchDir, filepath.FromSlash(options.Subdir))
	}
	pluginDir, err := locatePluginDir(sourceRoot)
	if err != nil {
		return InstallResult{}, err
	}

	manifestPath := filepath.Join(pluginDir, manifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return InstallResult{}, fmt.Errorf("read %s: %w", manifestFileName, err)
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return InstallResult{}, fmt.Errorf("parse %s: %w", manifestFileName, err)
	}

	// Validate against the same schema the loader uses. The install target id is
	// derived from the (validated) manifest id, so it is safe as a directory name.
	parsed, err := ParseManifest(raw, ParseManifestOptions{
		Source:       SourceUser,
		Root:         dir,
		PluginDir:    filepath.Join(dir, "pending"),
		ManifestPath: manifestPath,
	})
	if err != nil {
		return InstallResult{}, fmt.Errorf("invalid plugin manifest: %w", err)
	}
	id := parsed.ID
	if expectedID := strings.TrimSpace(options.ExpectedID); expectedID != "" && parsed.ID != expectedID {
		return InstallResult{}, fmt.Errorf("catalog expected plugin id %q, manifest has %q", expectedID, parsed.ID)
	}
	if expectedVersion := strings.TrimSpace(options.ExpectedVersion); expectedVersion != "" && parsed.Version != expectedVersion {
		return InstallResult{}, fmt.Errorf("catalog expected plugin version %q, manifest has %q", expectedVersion, parsed.Version)
	}
	if options.ExpectedComponents != nil {
		if err := compareInstallInventory(parsed, *options.ExpectedComponents); err != nil {
			return InstallResult{}, err
		}
	}

	// Hash the SAME filtered tree that copyTree installs (not just the manifest),
	// so a change to any installed file — a tool script, prompt, or bundled skill —
	// is reflected in the lock hash and reported as an update.
	hash, err := hashTree(pluginDir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("hash plugin: %w", err)
	}
	if expectedHash := strings.TrimSpace(options.ExpectedHash); expectedHash != "" && hash != expectedHash {
		return InstallResult{}, fmt.Errorf("catalog expected tree hash %s, filesystem has %s", expectedHash, hash)
	}

	lock, err := ReadLock(dir)
	if err != nil {
		return InstallResult{}, err
	}
	previous, existed := lock[id]
	if existed && previous.Source != source && !options.Force {
		return InstallResult{}, fmt.Errorf("%w: %q is installed from %s (use --force to overwrite)", ErrNameClash, id, previous.Source)
	}

	installDisabled := existed && previous.Enabled != nil && !*previous.Enabled
	target := filepath.Join(dir, id)
	if installDisabled {
		target = filepath.Join(dir, disabledDirName, id)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("create plugins dir: %w", err)
	}
	if installDisabled {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return InstallResult{}, fmt.Errorf("create disabled plugins dir: %w", err)
		}
	}
	stage, err := os.MkdirTemp(dir, "."+id+"-stage-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("create staged plugin dir: %w", err)
	}
	stagePublished := false
	defer func() {
		if !stagePublished {
			_ = os.RemoveAll(stage)
		}
	}()
	// Copy the whole plugin tree (entry scripts, prompts, skills) so the installed
	// plugin is runnable through activation. Copy DATA only — never execute it.
	if err := copyTree(pluginDir, stage); err != nil {
		return InstallResult{}, fmt.Errorf("copy plugin: %w", err)
	}
	stagedHash, err := hashTree(stage)
	if err != nil {
		return InstallResult{}, fmt.Errorf("hash staged plugin: %w", err)
	}
	if stagedHash != hash {
		return InstallResult{}, fmt.Errorf("staged plugin hash mismatch: source %s, staged %s", hash, stagedHash)
	}

	backup := ""
	if _, err := os.Stat(target); err == nil {
		backup = filepath.Join(dir, "."+id+"-backup-*")
		tempBackup, err := os.MkdirTemp(dir, "."+id+"-backup-")
		if err != nil {
			return InstallResult{}, fmt.Errorf("create backup dir: %w", err)
		}
		if err := os.Remove(tempBackup); err != nil {
			return InstallResult{}, fmt.Errorf("prepare backup dir: %w", err)
		}
		backup = tempBackup
		if err := os.Rename(target, backup); err != nil {
			return InstallResult{}, fmt.Errorf("backup previous plugin: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return InstallResult{}, fmt.Errorf("stat previous plugin: %w", err)
	}
	rolledBack := false
	rollbackPublish := func() {
		if rolledBack {
			return
		}
		rolledBack = true
		_ = os.RemoveAll(target)
		if backup != "" {
			_ = os.Rename(backup, target)
		}
	}
	if err := os.Rename(stage, target); err != nil {
		rollbackPublish()
		return InstallResult{}, fmt.Errorf("publish plugin: %w", err)
	}
	stagePublished = true

	enabled := true
	if previous.Enabled != nil {
		enabled = *previous.Enabled
	}
	lock[id] = LockEntry{
		Source:  source,
		Hash:    hash,
		Catalog: strings.TrimSpace(options.Catalog),
		Version: strings.TrimSpace(options.ExpectedVersion),
		Commit:  strings.TrimSpace(options.Commit),
		Subdir:  strings.TrimSpace(options.Subdir),
		Enabled: &enabled,
		Pinned:  options.Pinned,
	}
	if err := writeLock(dir, lock); err != nil {
		rollbackPublish()
		return InstallResult{}, err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}

	result := InstallResult{
		ID:           id,
		Name:         parsed.Name,
		Version:      parsed.Version,
		ManifestPath: filepath.Join(target, manifestFileName),
		Hash:         hash,
		Source:       source,
	}
	if existed {
		result.Updated = previous.Hash != hash
		result.PreviousHash = previous.Hash
	}
	return result, nil
}

// Remove deletes an installed plugin directory and its lockfile entry. It errors
// if the named plugin is not present in either the dir or the lockfile.
func Remove(dir string, id string) error {
	dir = strings.TrimSpace(dir)
	id = strings.TrimSpace(id)
	if dir == "" || id == "" {
		return errors.New("a plugins directory and plugin id are required")
	}
	if !validInstallID(id) {
		return fmt.Errorf("invalid plugin id %q", id)
	}

	return withPluginRootLock(dir, func() error {
		lock, err := ReadLock(dir)
		if err != nil {
			return err
		}
		_, locked := lock[id]
		target := filepath.Join(dir, id)
		disabledTarget := filepath.Join(dir, disabledDirName, id)
		present, err := dirExists(target)
		if err != nil {
			return fmt.Errorf("stat plugin dir: %w", err)
		}
		disabledPresent, err := dirExists(disabledTarget)
		if err != nil {
			return fmt.Errorf("stat disabled plugin dir: %w", err)
		}
		if !locked && !present && !disabledPresent {
			return fmt.Errorf("plugin %q is not installed", id)
		}
		if present {
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove plugin dir: %w", err)
			}
		}
		if disabledPresent {
			if err := os.RemoveAll(disabledTarget); err != nil {
				return fmt.Errorf("remove disabled plugin dir: %w", err)
			}
		}
		if locked {
			delete(lock, id)
			if err := writeLock(dir, lock); err != nil {
				return err
			}
		}
		return nil
	})
}

func VerifyInstalled(dir string, id string) (VerifyResult, error) {
	dir = strings.TrimSpace(dir)
	id = strings.TrimSpace(id)
	if dir == "" || id == "" {
		return VerifyResult{}, errors.New("a plugins directory and plugin id are required")
	}
	if !validInstallID(id) {
		return VerifyResult{}, fmt.Errorf("invalid plugin id %q", id)
	}
	lock, err := ReadLock(dir)
	if err != nil {
		return VerifyResult{}, err
	}
	entry, ok := lock[id]
	if !ok {
		return VerifyResult{}, fmt.Errorf("plugin %q is not managed by %s", id, LockFileName)
	}
	pluginDir := filepath.Join(dir, id)
	enabled := true
	if present, err := dirExists(pluginDir); err != nil {
		return VerifyResult{}, fmt.Errorf("stat plugin dir: %w", err)
	} else if !present {
		pluginDir = filepath.Join(dir, disabledDirName, id)
		enabled = false
		if present, err := dirExists(pluginDir); err != nil {
			return VerifyResult{}, fmt.Errorf("stat disabled plugin dir: %w", err)
		} else if !present {
			return VerifyResult{}, fmt.Errorf("plugin %q is not installed", id)
		}
	}
	hash, err := hashTree(pluginDir)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("hash plugin: %w", err)
	}
	if strings.TrimSpace(entry.Hash) == "" {
		return VerifyResult{}, fmt.Errorf("plugin %q has no integrity hash", id)
	}
	if hash != entry.Hash {
		return VerifyResult{}, fmt.Errorf("plugin integrity mismatch: lock has %s, filesystem has %s", entry.Hash, hash)
	}
	return VerifyResult{ID: id, Hash: hash, ExpectedHash: entry.Hash, Enabled: enabled}, nil
}

func Disable(dir string, id string) error {
	dir = strings.TrimSpace(dir)
	id = strings.TrimSpace(id)
	if dir == "" || id == "" {
		return errors.New("a plugins directory and plugin id are required")
	}
	if !validInstallID(id) {
		return fmt.Errorf("invalid plugin id %q", id)
	}
	return withPluginRootLock(dir, func() error {
		lock, err := ReadLock(dir)
		if err != nil {
			return err
		}
		entry := lock[id]
		target := filepath.Join(dir, id)
		quarantineRoot := filepath.Join(dir, disabledDirName)
		quarantine := filepath.Join(quarantineRoot, id)
		if _, err := os.Stat(target); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("plugin %q is not active", id)
			}
			return fmt.Errorf("stat active plugin: %w", err)
		}
		if _, err := os.Stat(quarantine); err == nil {
			return fmt.Errorf("disabled plugin %q already exists", id)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat disabled plugin: %w", err)
		}
		if err := verifyLockedHash(target, entry); err != nil {
			return err
		}
		if err := os.MkdirAll(quarantineRoot, 0o755); err != nil {
			return fmt.Errorf("create disabled plugins dir: %w", err)
		}
		if err := os.Rename(target, quarantine); err != nil {
			return fmt.Errorf("quarantine plugin: %w", err)
		}
		enabled := false
		entry.Enabled = &enabled
		lock[id] = entry
		if err := writeLock(dir, lock); err != nil {
			_ = os.Rename(quarantine, target)
			return err
		}
		return nil
	})
}

func Enable(dir string, id string) error {
	dir = strings.TrimSpace(dir)
	id = strings.TrimSpace(id)
	if dir == "" || id == "" {
		return errors.New("a plugins directory and plugin id are required")
	}
	if !validInstallID(id) {
		return fmt.Errorf("invalid plugin id %q", id)
	}
	return withPluginRootLock(dir, func() error {
		lock, err := ReadLock(dir)
		if err != nil {
			return err
		}
		entry := lock[id]
		target := filepath.Join(dir, id)
		quarantine := filepath.Join(dir, disabledDirName, id)
		if _, err := os.Stat(quarantine); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("plugin %q is not disabled", id)
			}
			return fmt.Errorf("stat disabled plugin: %w", err)
		}
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("active plugin %q already exists", id)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat active plugin: %w", err)
		}
		if err := verifyLockedHash(quarantine, entry); err != nil {
			return err
		}
		if err := os.Rename(quarantine, target); err != nil {
			return fmt.Errorf("enable plugin: %w", err)
		}
		enabled := true
		entry.Enabled = &enabled
		lock[id] = entry
		if err := writeLock(dir, lock); err != nil {
			_ = os.Rename(target, quarantine)
			return err
		}
		return nil
	})
}

func Pin(dir string, id string, version string) (LockEntry, error) {
	dir = strings.TrimSpace(dir)
	id = strings.TrimSpace(id)
	if dir == "" || id == "" {
		return LockEntry{}, errors.New("a plugins directory and plugin id are required")
	}
	if !validInstallID(id) {
		return LockEntry{}, fmt.Errorf("invalid plugin id %q", id)
	}
	var pinned LockEntry
	err := withPluginRootLock(dir, func() error {
		lock, err := ReadLock(dir)
		if err != nil {
			return err
		}
		entry, ok := lock[id]
		if !ok {
			return fmt.Errorf("plugin %q is not installed", id)
		}
		entry.Pinned = true
		requestedVersion := strings.TrimSpace(version)
		if requestedVersion != "" {
			installedVersion, err := installedPluginVersion(dir, id)
			if err != nil {
				return err
			}
			if requestedVersion != installedVersion {
				return fmt.Errorf("requested pin version %q does not match installed version %q", requestedVersion, installedVersion)
			}
			entry.Version = requestedVersion
		}
		lock[id] = entry
		if err := writeLock(dir, lock); err != nil {
			return err
		}
		pinned = entry
		return nil
	})
	return pinned, err
}

func Unpin(dir string, id string) (LockEntry, error) {
	dir = strings.TrimSpace(dir)
	id = strings.TrimSpace(id)
	if dir == "" || id == "" {
		return LockEntry{}, errors.New("a plugins directory and plugin id are required")
	}
	if !validInstallID(id) {
		return LockEntry{}, fmt.Errorf("invalid plugin id %q", id)
	}
	var unpinned LockEntry
	err := withPluginRootLock(dir, func() error {
		lock, err := ReadLock(dir)
		if err != nil {
			return err
		}
		entry, ok := lock[id]
		if !ok {
			return fmt.Errorf("plugin %q is not installed", id)
		}
		entry.Pinned = false
		lock[id] = entry
		if err := writeLock(dir, lock); err != nil {
			return err
		}
		unpinned = entry
		return nil
	})
	return unpinned, err
}

// ReadLock loads the plugins lockfile from dir. A missing lockfile yields an
// empty map with no error.
func ReadLock(dir string) (map[string]LockEntry, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return map[string]LockEntry{}, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, LockFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]LockEntry{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", LockFileName, err)
	}
	entries := map[string]LockEntry{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return entries, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", LockFileName, err)
	}
	return entries, nil
}

func dirExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

func verifyLockedHash(pluginDir string, entry LockEntry) error {
	if strings.TrimSpace(entry.Hash) == "" {
		return nil
	}
	hash, err := hashTree(pluginDir)
	if err != nil {
		return fmt.Errorf("hash plugin: %w", err)
	}
	if hash != entry.Hash {
		return fmt.Errorf("plugin integrity mismatch: lock has %s, filesystem has %s", entry.Hash, hash)
	}
	return nil
}

func compareInstallInventory(plugin LoadedPlugin, expected InstallComponentInventory) error {
	actualTools := make([]string, 0, len(plugin.Tools))
	for _, tool := range plugin.Tools {
		actualTools = append(actualTools, tool.Name+":"+string(tool.Permission))
	}
	expectedTools := make([]string, 0, len(expected.Tools))
	for _, tool := range expected.Tools {
		expectedTools = append(expectedTools, tool.Name+":"+string(tool.Permission))
	}
	if !sameSortedStrings(actualTools, expectedTools) {
		return fmt.Errorf("catalog component inventory mismatch for tools: expected %v, manifest has %v", sortedStrings(expectedTools), sortedStrings(actualTools))
	}

	actualHooks := make([]string, 0, len(plugin.Hooks))
	for _, hook := range plugin.Hooks {
		actualHooks = append(actualHooks, hook.Name+":"+string(hook.Event))
	}
	expectedHooks := make([]string, 0, len(expected.Hooks))
	for _, hook := range expected.Hooks {
		expectedHooks = append(expectedHooks, hook.Name+":"+string(hook.Event))
	}
	if !sameSortedStrings(actualHooks, expectedHooks) {
		return fmt.Errorf("catalog component inventory mismatch for hooks: expected %v, manifest has %v", sortedStrings(expectedHooks), sortedStrings(actualHooks))
	}

	actualSkills := make([]string, 0, len(plugin.Skills))
	for _, skill := range plugin.Skills {
		actualSkills = append(actualSkills, skill.Name)
	}
	if !sameSortedStrings(actualSkills, expected.Skills) {
		return fmt.Errorf("catalog component inventory mismatch for skills: expected %v, manifest has %v", sortedStrings(expected.Skills), sortedStrings(actualSkills))
	}

	actualPrompts := make([]string, 0, len(plugin.Prompts))
	for _, prompt := range plugin.Prompts {
		actualPrompts = append(actualPrompts, prompt.Name)
	}
	if !sameSortedStrings(actualPrompts, expected.Prompts) {
		return fmt.Errorf("catalog component inventory mismatch for prompts: expected %v, manifest has %v", sortedStrings(expected.Prompts), sortedStrings(actualPrompts))
	}
	return nil
}

func sameSortedStrings(left []string, right []string) bool {
	left = sortedStrings(left)
	right = sortedStrings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortedStrings(values []string) []string {
	copied := append([]string{}, values...)
	sort.Strings(copied)
	return copied
}

func withPluginRootLock(dir string, fn func() error) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create plugins dir: %w", err)
	}
	lock, err := acquirePluginRootLock(dir)
	if err != nil {
		return err
	}
	defer lock.release()
	return fn()
}

func writeLock(dir string, entries map[string]LockEntry) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create plugins dir: %w", err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", LockFileName, err)
	}
	lockPath := filepath.Join(dir, LockFileName)
	temp, err := os.CreateTemp(dir, "."+LockFileName+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create %s temp: %w", LockFileName, err)
	}
	tempName := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write %s temp: %w", LockFileName, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync %s temp: %w", LockFileName, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close %s temp: %w", LockFileName, err)
	}
	if err := os.Rename(tempName, lockPath); err != nil {
		return fmt.Errorf("replace %s: %w", LockFileName, err)
	}
	cleanup = false
	_ = syncDir(dir)
	return nil
}

func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

// fetchSource resolves a source into a local directory. A local path is used in
// place; a git URL is shallow-cloned into a temp dir via the runner.
func fetchSource(ctx context.Context, source string, runner GitRunner) (string, func(), error) {
	if isLocalPath(source) {
		info, err := os.Stat(source)
		if err != nil {
			return "", func() {}, fmt.Errorf("read source: %w", err)
		}
		if !info.IsDir() {
			return "", func() {}, fmt.Errorf("source must be a directory: %s", source)
		}
		abs, err := filepath.Abs(source)
		if err != nil {
			return "", func() {}, err
		}
		return abs, func() {}, nil
	}

	if runner == nil {
		runner = defaultGitRunner
	}
	temp, err := os.MkdirTemp("", "zero-plugin-fetch-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	gitCtx, cancel := pluginGitFetchContext(ctx)
	defer cancel()
	if err := runner(gitCtx, temp, source); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("fetch %s: %w", source, err)
	}
	return temp, cleanup, nil
}

// defaultGitRunner shallow-clones source into destination. exec.CommandContext
// inherits the process environment, so proxy/egress settings are honored;
// GIT_TERMINAL_PROMPT=0 prevents a credential prompt from blocking. Cloning only
// fetches; it never executes repository content.
func defaultGitRunner(ctx context.Context, destination string, source string) error {
	command := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--", source, destination)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func checkoutCommit(ctx context.Context, repo string, commit string) error {
	if !installCommitPattern.MatchString(commit) {
		return fmt.Errorf("commit must be a 40-character git SHA")
	}
	gitCtx, cancel := pluginGitFetchContext(ctx)
	defer cancel()
	fetch := exec.CommandContext(gitCtx, "git", "-C", repo, "fetch", "--depth", "1", "origin", commit)
	fetch.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch commit failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	checkout := exec.CommandContext(gitCtx, "git", "-C", repo, "checkout", "--detach", commit)
	checkout.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout commit failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func pluginGitFetchContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, pluginGitFetchTimeout)
}

// locatePluginDir finds the directory holding plugin.json within root: the root
// itself, or exactly one immediate subdirectory.
func locatePluginDir(root string) (string, error) {
	if fileExists(filepath.Join(root, manifestFileName)) {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("scan source: %w", err)
	}
	matches := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		if fileExists(filepath.Join(candidate, manifestFileName)) {
			matches = append(matches, candidate)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no %s found in source", manifestFileName)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("source contains multiple plugins (%d); install one at a time", len(matches))
	}
}

// copyTree recursively copies regular files and directories from src to dst. It
// skips the .git directory (clone metadata) and refuses symlinks so a malicious
// source cannot smuggle a link that escapes the install dir. Copying is pure
// I/O — it never executes anything it copies.
func copyTree(src string, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		srcPath := filepath.Join(src, name)
		dstPath := filepath.Join(dst, name)
		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			// Never recreate a symlink: it could point outside the install dir and
			// turn a copy into a write/read primitive elsewhere.
			continue
		case info.IsDir():
			if err := copyTree(srcPath, dstPath); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err := copyFile(srcPath, dstPath, info.Mode().Perm()); err != nil {
				return err
			}
		default:
			// Skip FIFOs, sockets, devices.
			continue
		}
	}
	return nil
}

func copyFile(src string, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func installedPluginVersion(dir string, id string) (string, error) {
	for _, pluginDir := range []string{
		filepath.Join(dir, id),
		filepath.Join(dir, disabledDirName, id),
	} {
		data, err := os.ReadFile(filepath.Join(pluginDir, manifestFileName))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read installed plugin manifest: %w", err)
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return "", fmt.Errorf("parse installed plugin manifest: %w", err)
		}
		version := strings.TrimSpace(manifest.Version)
		if version == "" {
			return "", fmt.Errorf("installed plugin %q has no version", id)
		}
		return version, nil
	}
	return "", fmt.Errorf("plugin %q is not installed", id)
}

// canonicalSource normalizes a local filesystem source to an absolute,
// symlink-evaluated path so a re-install via a different spelling of the same
// directory is recognized as the same source. Remote sources (git URLs) are
// returned unchanged. On any resolution error the input is returned as-is so a
// non-existent local path still surfaces its real error later in fetchSource.
func canonicalSource(source string) string {
	if !isLocalPath(source) {
		return source
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return source
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// isLocalPath reports whether source is a local filesystem path rather than a
// remote URL. URLs (scheme://… or scp-style host:path) and git shorthand are
// remote.
func isLocalPath(source string) bool {
	if source == "" {
		return false
	}
	if strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~") {
		return true
	}
	if filepath.IsAbs(source) {
		return true
	}
	if hasURLScheme(source) {
		return false
	}
	if idx := strings.Index(source, ":"); idx > 0 {
		host := source[:idx]
		if strings.Contains(host, "@") {
			return false
		}
		if len(host) == 1 {
			return true // drive letter
		}
		if strings.Contains(host, ".") {
			return false // hostname
		}
	}
	return true
}

func hasURLScheme(source string) bool {
	for _, scheme := range []string{"http://", "https://", "git://", "ssh://", "git+ssh://", "ftp://", "ftps://", "file://"} {
		if strings.HasPrefix(strings.ToLower(source), scheme) {
			return true
		}
	}
	return false
}

// validInstallID guards a plugin id used as a directory component. Manifest ids
// already match pluginIDPattern, but Remove takes an id directly from the user.
func validInstallID(id string) bool {
	if !pluginIDPattern.MatchString(id) {
		return false
	}
	return id == filepath.Base(id) && !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

// hashTree computes a content hash over the same filtered tree that copyTree
// installs: regular files only, .git and symlinks skipped, walked in a stable
// sorted order. Each file contributes its plugin-relative path, executable bit,
// and bytes, so renames, mode flips, and content edits all change the hash.
func hashTree(root string) (string, error) {
	hasher := sha256.New()
	if err := hashTreeInto(hasher, root, root); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func hashTreeInto(hasher io.Writer, root string, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if name == ".git" {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			// Skipped by copyTree, so excluded from the hash too.
			continue
		case info.IsDir():
			if err := hashTreeInto(hasher, root, path); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			executable := 0
			if info.Mode().Perm()&0o111 != 0 {
				executable = 1
			}
			// Null-delimited header keeps file boundaries unambiguous (paths cannot
			// contain null bytes) so two trees cannot collide by shifting bytes.
			header := fmt.Sprintf("%s\x00%d\x00", filepath.ToSlash(rel), executable)
			if _, err := io.WriteString(hasher, header); err != nil {
				return err
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			if _, err := io.Copy(hasher, file); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		default:
			// FIFOs, sockets, devices: skipped by copyTree, excluded here.
			continue
		}
	}
	return nil
}
