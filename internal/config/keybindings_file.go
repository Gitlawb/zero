package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KeybindingsFile is the on-disk schema for keybindings.json (leader prefix +
// slash-command chords). Global toggles stay in config.json.
type KeybindingsFile struct {
	LeaderKey string            `json:"leaderKey,omitempty"`
	Leader    map[string]string `json:"leader,omitempty"`
}

// DefaultLeaderKey is the built-in leader prefix.
const DefaultLeaderKey = "ctrl+x"

// DefaultLeaderAssignments maps primary slash commands to their built-in
// second-key letters (slash → letter). Unlisted builtins seed as "".
func DefaultLeaderAssignments() map[string]string {
	return map[string]string{
		"/model":     "m",
		"/provider":  "p",
		"/plan":      "P",
		"/stt-model": "M",
		"/voice":     "v",
		"/clear":     "c",
		"/context":   "C",
		"/stop":      "s",
		"/image":     "i",
		"/resume":    "r",
		"/rewind":    "u",
		"/tools":     "t",
		"/retry":     "R",
	}
}

// primarySlashesForSeed is set by SetKeybindingsPrimarySlashes so config writers
// can seed a full catalog without importing the TUI package.
var primarySlashesForSeed []string

// SetKeybindingsPrimarySlashes registers the full primary slash catalog used when
// auto-seeding keybindings.json next to config.json. Call once at process start
// (CLI) with tui.PrimarySlashNames().
func SetKeybindingsPrimarySlashes(names []string) {
	primarySlashesForSeed = append([]string(nil), names...)
}

// KeybindingsPrimarySlashes returns the registered seed catalog (may be empty
// before SetKeybindingsPrimarySlashes).
func KeybindingsPrimarySlashes() []string {
	return append([]string(nil), primarySlashesForSeed...)
}

// DefaultKeybindingsFile builds the seed document: leaderKey + every primary
// slash (assigned letters from DefaultLeaderAssignments, else "").
// If primarySlashes is empty, falls back to KeybindingsPrimarySlashes, then to
// only the assigned defaults.
func DefaultKeybindingsFile(primarySlashes []string) KeybindingsFile {
	names := primarySlashes
	if len(names) == 0 {
		names = primarySlashesForSeed
	}
	if len(names) == 0 {
		for slash := range DefaultLeaderAssignments() {
			names = append(names, slash)
		}
		sort.Strings(names)
	}
	assignments := DefaultLeaderAssignments()
	leader := make(map[string]string, len(names))
	for _, slash := range names {
		slash = strings.TrimSpace(slash)
		if slash == "" {
			continue
		}
		if letter, ok := assignments[slash]; ok {
			leader[slash] = letter
		} else {
			leader[slash] = ""
		}
	}
	// Ensure every default assignment is present even if catalog omitted it.
	for slash, letter := range assignments {
		if _, ok := leader[slash]; !ok {
			leader[slash] = letter
		}
	}
	return KeybindingsFile{
		LeaderKey: DefaultLeaderKey,
		Leader:    leader,
	}
}

// KeybindingsPathBeside returns the keybindings.json path next to a config.json path.
func KeybindingsPathBeside(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "keybindings.json")
}

// DefaultUserKeybindingsPath is the user-level keybindings.json (sibling of config.json).
func DefaultUserKeybindingsPath() (string, error) {
	configPath, err := DefaultUserConfigPath()
	if err != nil {
		return "", err
	}
	return KeybindingsPathBeside(configPath), nil
}

// ProjectKeybindingsPath is <workspace>/.zero/keybindings.json.
func ProjectKeybindingsPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".zero", "keybindings.json")
}

// EnsureKeybindingsFile writes seed JSON if path is missing. Never overwrites.
// Creation uses O_CREATE|O_EXCL so concurrent processes racing to seed the same
// path cannot replace a file another process (or the user) already wrote.
func EnsureKeybindingsFile(path string, seed KeybindingsFile) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty keybindings path")
	}
	// Fast path: already present.
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect keybindings %s: %w", path, err)
	}
	if seed.Leader == nil {
		seed = DefaultKeybindingsFile(nil)
	}
	if strings.TrimSpace(seed.LeaderKey) == "" {
		seed.LeaderKey = DefaultLeaderKey
	}
	return createKeybindingsFileIfAbsent(path, seed)
}

// EnsureKeybindingsBesideConfig seeds keybindings.json next to configPath when absent.
func EnsureKeybindingsBesideConfig(configPath string) error {
	path := KeybindingsPathBeside(configPath)
	if path == "" {
		return nil
	}
	return EnsureKeybindingsFile(path, DefaultKeybindingsFile(nil))
}

// LoadKeybindingsFile reads a keybindings.json. Missing file returns empty
// KeybindingsFile and nil error (caller merges with defaults).
func LoadKeybindingsFile(path string) (KeybindingsFile, error) {
	if strings.TrimSpace(path) == "" {
		return KeybindingsFile{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return KeybindingsFile{}, nil
		}
		return KeybindingsFile{}, fmt.Errorf("read keybindings %s: %w", path, err)
	}
	var file KeybindingsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return KeybindingsFile{}, fmt.Errorf("parse keybindings %s: %w", path, err)
	}
	if file.Leader == nil {
		file.Leader = map[string]string{}
	}
	return file, nil
}

// ResolveKeybindings overlays user then project on defaults.
// Omitted leaderKey keeps previous; present leader keys overlay (including "").
func ResolveKeybindings(defaults, user, project KeybindingsFile) KeybindingsFile {
	out := cloneKeybindingsFile(defaults)
	if out.Leader == nil {
		out.Leader = map[string]string{}
	}
	if strings.TrimSpace(out.LeaderKey) == "" {
		out.LeaderKey = DefaultLeaderKey
	}
	overlayKeybindings(&out, user)
	overlayKeybindings(&out, project)
	return out
}

func overlayKeybindings(dst *KeybindingsFile, src KeybindingsFile) {
	if key := strings.TrimSpace(src.LeaderKey); key != "" {
		dst.LeaderKey = key
	}
	if dst.Leader == nil {
		dst.Leader = map[string]string{}
	}
	for slash, letter := range src.Leader {
		dst.Leader[slash] = letter
	}
}

func cloneKeybindingsFile(in KeybindingsFile) KeybindingsFile {
	out := KeybindingsFile{LeaderKey: in.LeaderKey}
	if in.Leader != nil {
		out.Leader = make(map[string]string, len(in.Leader))
		for k, v := range in.Leader {
			out.Leader[k] = v
		}
	}
	return out
}

// encodeKeybindingsJSON returns indented JSON bytes for file (with defaults filled).
func encodeKeybindingsJSON(file KeybindingsFile) ([]byte, error) {
	out := file
	if strings.TrimSpace(out.LeaderKey) == "" {
		out.LeaderKey = DefaultLeaderKey
	}
	if out.Leader == nil {
		out.Leader = map[string]string{}
	}
	// Stable key order for readable diffs (maps encode in insertion order).
	sortedLeader := make(map[string]string, len(out.Leader))
	keys := make([]string, 0, len(out.Leader))
	for k := range out.Leader {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sortedLeader[k] = out.Leader[k]
	}
	out.Leader = sortedLeader

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode keybindings JSON: %w", err)
	}
	return append(data, '\n'), nil
}

// createKeybindingsFileIfAbsent writes seed only when path does not exist.
// Uses O_CREATE|O_EXCL so a concurrent creator that lost the race treats the
// existing file as success and does not overwrite it (unlike rename).
func createKeybindingsFileIfAbsent(path string, file KeybindingsFile) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create keybindings directory %s: %w", dir, err)
		}
	}
	data, err := encodeKeybindingsJSON(file)
	if err != nil {
		return err
	}
	// Exclusive create: fails with ErrExist if another process won the race or
	// the user already has a file. Never truncate/replace an existing path.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("create keybindings %s: %w", path, err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(path) // leave no partial seed behind
		return fmt.Errorf("write keybindings %s: %w", path, writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write keybindings %s: %w", path, closeErr)
	}
	return nil
}

// writeKeybindingsFile overwrites path (tests / explicit rewrites only).
// Prefer createKeybindingsFileIfAbsent for user-facing seed paths.
func writeKeybindingsFile(path string, file KeybindingsFile) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create keybindings directory %s: %w", dir, err)
		}
	}
	data, err := encodeKeybindingsJSON(file)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".zero-keybindings-*.tmp")
	if err != nil {
		return fmt.Errorf("write keybindings %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure keybindings permissions %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write keybindings %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write keybindings %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("write keybindings %s: %w", path, err)
	}
	return nil
}
