package execution

import (
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	maxObservedFiles    = 20_000
	maxHashedFileBytes  = 1 << 20
	maxTotalHashedBytes = 32 << 20
)

type ChangeKind string

const (
	ChangeCreated  ChangeKind = "created"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

type Change struct {
	Path       string     `json:"path"`
	Kind       ChangeKind `json:"kind"`
	Aggregated bool       `json:"aggregated,omitempty"`
	Count      int        `json:"count,omitempty"`
}

type fileFingerprint struct {
	Mode    fs.FileMode
	Size    int64
	ModTime int64
	Digest  [sha256.Size]byte
	Hashed  bool
	// Aggregated marks a bounded fingerprint for a generated directory. The
	// observer never enumerates that tree into individual changes.
	Aggregated bool
	Count      int
}

// ChangeObserver records a bounded workspace snapshot around a command. It
// deliberately skips Zero/repository control metadata and large generated
// dependency trees; those paths must neither be read as command evidence nor
// flood the Files panel.
type ChangeObserver struct {
	root   string
	before map[string]fileFingerprint
	valid  bool
}

func NewChangeObserver(root string) *ChangeObserver {
	root = filepath.Clean(strings.TrimSpace(root))
	before, ok := snapshotWorkspace(root)
	return &ChangeObserver{root: root, before: before, valid: ok}
}

func (observer *ChangeObserver) Changes() []Change {
	if observer == nil || !observer.valid {
		return nil
	}
	after, ok := snapshotWorkspace(observer.root)
	if !ok {
		return nil
	}
	changes := make([]Change, 0)
	for path, current := range after {
		previous, existed := observer.before[path]
		if !existed {
			changes = append(changes, changeFromFingerprint(path, ChangeCreated, current))
			continue
		}
		if previous != current {
			changes = append(changes, changeFromFingerprint(path, ChangeModified, current))
		}
	}
	for path, previous := range observer.before {
		if _, exists := after[path]; !exists {
			changes = append(changes, changeFromFingerprint(path, ChangeDeleted, previous))
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func snapshotWorkspace(root string) (map[string]fileFingerprint, bool) {
	if root == "" || root == "." {
		return nil, false
	}
	files := make(map[string]fileFingerprint)
	hashedBytes := int64(0)
	ok := true
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil
		}
		if entry.IsDir() {
			if protectedObservationDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			if generatedObservationDirectory(entry.Name()) {
				files[filepath.ToSlash(relative)+"/"] = generatedTreeFingerprint(path)
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if len(files) >= maxObservedFiles {
			ok = false
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		fingerprint := fileFingerprint{Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if info.Size() <= maxHashedFileBytes && hashedBytes+info.Size() <= maxTotalHashedBytes {
			if digest, err := hashObservedFile(path); err == nil {
				fingerprint.Digest = digest
				fingerprint.Hashed = true
				hashedBytes += info.Size()
			}
		}
		files[filepath.ToSlash(relative)] = fingerprint
		return nil
	})
	if err != nil {
		return nil, false
	}
	return files, ok
}

func protectedObservationDirectory(name string) bool {
	switch name {
	case ".git", ".zero", ".agents":
		return true
	default:
		return false
	}
}

func generatedObservationDirectory(name string) bool {
	switch name {
	case "node_modules", ".pnpm-store", ".yarn", "dist", "coverage", ".cache":
		return true
	default:
		return false
	}
}

// generatedTreeFingerprint samples only direct children and caps the work. It
// is intentionally qualitative: the UI needs to say that a generated tree
// changed, not inventory dependency contents.
func generatedTreeFingerprint(root string) fileFingerprint {
	directory, err := os.Open(root)
	if err != nil {
		return fileFingerprint{Aggregated: true, Count: -1}
	}
	defer directory.Close()
	const maxEntries = 256
	entries, err := directory.ReadDir(maxEntries + 1)
	if err != nil && err != io.EOF {
		return fileFingerprint{Aggregated: true, Count: -1}
	}
	hash := sha256.New()
	count := len(entries)
	if count > maxEntries {
		entries = entries[:maxEntries]
		count = -1
	}
	for _, entry := range entries {
		_, _ = io.WriteString(hash, entry.Name())
		if info, infoErr := entry.Info(); infoErr == nil {
			_, _ = io.WriteString(hash, info.Mode().String())
			_, _ = io.WriteString(hash, strconv.FormatInt(info.ModTime().UnixNano(), 10))
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return fileFingerprint{Digest: digest, Hashed: true, Aggregated: true, Count: count}
}

func changeFromFingerprint(path string, kind ChangeKind, fingerprint fileFingerprint) Change {
	return Change{Path: path, Kind: kind, Aggregated: fingerprint.Aggregated, Count: fingerprint.Count}
}

func hashObservedFile(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}
