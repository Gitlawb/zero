package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"time"
)

// ErrFileChangedOnDisk is returned by CheckConflict when a file's current
// on-disk content no longer matches what a tool last read or wrote in this
// session — i.e. it was modified outside Zero. The write tools surface it with
// guidance to re-read before retrying, so a stale overwrite cannot silently
// clobber an external edit.
var ErrFileChangedOnDisk = errors.New("the file changed on disk since you last read it")

// FileVersion is what a tool last observed for a file: an authoritative content
// hash plus cheap stat metadata kept for diagnostics.
type FileVersion struct {
	Hash  string
	Size  int64
	MTime time.Time
}

// FileTracker records, per absolute path, the version of each file a tool read
// or wrote within a single session. A later write compares the current on-disk
// content against this baseline to detect a modification made outside Zero.
//
// Construct with NewFileTracker. A nil *FileTracker is a valid no-op — every
// method tolerates it — so a caller that has not wired the feature (tests, MCP)
// needs no nil guards.
type FileTracker struct {
	mu       sync.Mutex
	versions map[string]FileVersion
}

func NewFileTracker() *FileTracker {
	return &FileTracker{versions: make(map[string]FileVersion)}
}

// Record stores the version of absPath given its content and optional stat info.
func (tracker *FileTracker) Record(absPath string, content []byte, info os.FileInfo) {
	if tracker == nil {
		return
	}
	version := FileVersion{Hash: HashContent(content)}
	if info != nil {
		version.Size = info.Size()
		version.MTime = info.ModTime()
	}
	tracker.mu.Lock()
	tracker.versions[absPath] = version
	tracker.mu.Unlock()
}

// Version returns the recorded version for absPath and whether one exists.
func (tracker *FileTracker) Version(absPath string) (FileVersion, bool) {
	if tracker == nil {
		return FileVersion{}, false
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	version, ok := tracker.versions[absPath]
	return version, ok
}

// Forget drops any recorded version for absPath, so the next read re-establishes
// the baseline. Used after a tool rewrites a file by a path other than its own
// (e.g. apply_patch) where the new content is not cheaply available to Record.
func (tracker *FileTracker) Forget(absPath string) {
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	delete(tracker.versions, absPath)
	tracker.mu.Unlock()
}

// CheckConflict reports ErrFileChangedOnDisk when absPath has a recorded baseline
// and currentContent (the bytes the caller just read from disk) no longer matches
// it. An untracked path returns nil: with no baseline there is nothing to
// conflict against, so a first-touch write is always allowed.
func (tracker *FileTracker) CheckConflict(absPath string, currentContent []byte) error {
	if tracker == nil {
		return nil
	}
	version, ok := tracker.Version(absPath)
	if !ok {
		return nil
	}
	if HashContent(currentContent) != version.Hash {
		return ErrFileChangedOnDisk
	}
	return nil
}

// HashContent returns the hex-encoded SHA-256 of content. Exported so the write
// tools and tests share one definition of a file's identity.
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// fileConflictMessage is the actionable error a write tool returns when a target
// changed on disk since it was last read, telling the model how to recover.
func fileConflictMessage(relativePath string) string {
	return "Error writing " + relativePath + ": " + ErrFileChangedOnDisk.Error() +
		" (it may have been edited outside Zero). Re-read it with read_file, then re-apply your change so you do not overwrite the newer content."
}
