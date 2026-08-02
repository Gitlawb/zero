package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"strings"
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
	seen     map[string]fileObservation
	// created preserves the order in which brand-new files (paths that did not
	// exist before a write tool created them) were first written this session.
	// A map would lose that order and complicates de-duplication, so a slice
	// plus a seen-set is used instead.
	created     []string
	createdSeen map[string]bool
}

func NewFileTracker() *FileTracker {
	return &FileTracker{
		versions: make(map[string]FileVersion),
		seen:     make(map[string]fileObservation),
	}
}

type lineRange struct {
	start int
	end   int
}

type fileObservation struct {
	whole bool
	// total is the file's line count as of the last recorded read, so coverage
	// can be judged from the ranges alone. Without it a file read in pieces
	// could never be known to have been seen in full.
	total      int
	ranges     []lineRange
	totalBytes int
	byteRanges []lineRange // zero-based, half-open byte intervals
}

type pendingFileObservation struct {
	path     string
	output   string
	hash     string
	start    int
	end      int
	total    int
	byteMode bool
}

// Record stores the version of absPath given its content and optional stat info.
func (tracker *FileTracker) Record(absPath string, content []byte, info os.FileInfo) {
	tracker.RecordHash(absPath, HashContent(content), info)
}

// RecordHash stores the version of absPath when the caller already computed
// the same HashContent-compatible SHA-256 while reading the file.
func (tracker *FileTracker) RecordHash(absPath string, hash string, info os.FileInfo) {
	if tracker == nil {
		return
	}
	version := FileVersion{Hash: hash}
	if info != nil {
		version.Size = info.Size()
		version.MTime = info.ModTime()
	}
	tracker.mu.Lock()
	if previous, ok := tracker.versions[absPath]; !ok || previous.Hash != hash {
		delete(tracker.seen, absPath)
	}
	tracker.versions[absPath] = version
	tracker.mu.Unlock()
}

// RecordSeenRange records exact, non-elided source lines returned to the model.
// Ranges accumulate while the content hash remains unchanged.
func (tracker *FileTracker) RecordSeenRange(absPath string, start, end, total int) {
	if tracker == nil || start < 1 {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	observation := tracker.seen[absPath]
	if total == 0 {
		observation.whole = true
		observation.ranges = nil
		tracker.seen[absPath] = observation
		return
	}
	if end < start {
		return
	}
	// A new line count means the file changed shape since the last read, so
	// previously recorded ranges no longer describe it.
	if observation.total != 0 && observation.total != total {
		observation = fileObservation{}
	}
	observation.total = total
	if start == 1 && end >= total {
		observation.whole = true
		observation.ranges = nil
		tracker.seen[absPath] = observation
		return
	}
	observation.ranges = append(observation.ranges, lineRange{start: start, end: end})
	tracker.seen[absPath] = observation
}

// RecordSeenBytes records an exact, non-elided byte interval returned to the
// model. Byte intervals make oversized single-line files recoverable without
// crediting the model for the omitted middle of a truncated line read.
func (tracker *FileTracker) RecordSeenBytes(absPath string, start, end, total int) {
	if tracker == nil || start < 0 || end < start || total < 0 || end > total {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	observation := tracker.seen[absPath]
	if total == 0 {
		observation.whole = true
		observation.byteRanges = nil
		tracker.seen[absPath] = observation
		return
	}
	if observation.totalBytes != 0 && observation.totalBytes != total {
		observation = fileObservation{}
	}
	observation.totalBytes = total
	if start == 0 && end >= total {
		observation.whole = true
		observation.byteRanges = nil
		tracker.seen[absPath] = observation
		return
	}
	observation.byteRanges = append(observation.byteRanges, lineRange{start: start, end: end})
	tracker.seen[absPath] = observation
}

// RecordEdit re-baselines absPath after an edit THIS SESSION made, keeping the
// reads the edit did not disturb.
//
// WHY THIS EXISTS RATHER THAN Record. RecordHash drops every recorded range when
// the content hash moves, which is right for a change we did not make: we cannot
// say which lines still hold what was read. After our own edit we can say
// exactly. The content before the first changed line is byte-identical and sits
// at the same line numbers; the content after the last changed line is
// byte-identical and has moved by a known delta. Only the lines the edit
// actually spans stop describing the file.
//
// Dropping the lot instead cost a real run. A 371-line file was read in three
// pieces (40-45, 85-260, 260-371) and one two-line edit at line 92 erased the
// credit for all three: the next six edits — into regions that had been read,
// that the edit did not touch, in a file whose line count had not changed — were
// each refused as content "not read in this session", and the repeated-failure
// guard halted the run. The error even told the model to re-read, which would
// have been undone by its next successful edit. The guard is right that a model
// must not edit what it has not seen; it was wrong about what it had seen.
func (tracker *FileTracker) RecordEdit(absPath string, before, after []byte, info os.FileInfo) {
	if tracker == nil {
		return
	}
	firstLine, lastLineBefore, lineDelta := changedLineSpan(string(before), string(after))
	firstByte, lastByteBefore, byteDelta := changedByteSpan(before, after)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	version := FileVersion{Hash: HashContent(after)}
	if info != nil {
		version.Size = info.Size()
		version.MTime = info.ModTime()
	}
	tracker.versions[absPath] = version

	observation, tracked := tracker.seen[absPath]
	if !tracked {
		return
	}
	if observation.whole {
		// Still whole: every line was read, and an edit of ours does not make
		// that untrue.
		observation.total = countLines(after)
		observation.totalBytes = len(after)
		tracker.seen[absPath] = observation
		return
	}

	// SPLIT AROUND THE EDIT, never drop the whole range. A read almost always
	// SPANS the line it is about to edit — that is why it was read — so dropping
	// on overlap would have thrown away 85-260 to change line 92 and left the
	// original defect in place under a longer implementation.
	kept := make([]lineRange, 0, len(observation.ranges)+1)
	for _, seen := range observation.ranges {
		if end := min(seen.end, firstLine-1); seen.start <= end {
			kept = append(kept, lineRange{start: seen.start, end: end})
		}
		if start := max(seen.start, lastLineBefore+1); start <= seen.end {
			kept = append(kept, lineRange{start: start + lineDelta, end: seen.end + lineDelta})
		}
	}
	observation.ranges = kept
	if observation.total != 0 {
		observation.total = countLines(after)
	}

	// Same split, on half-open byte intervals.
	keptBytes := make([]lineRange, 0, len(observation.byteRanges)+1)
	for _, seen := range observation.byteRanges {
		if end := min(seen.end, firstByte); seen.start < end {
			keptBytes = append(keptBytes, lineRange{start: seen.start, end: end})
		}
		if start := max(seen.start, lastByteBefore); start < seen.end {
			keptBytes = append(keptBytes, lineRange{start: start + byteDelta, end: seen.end + byteDelta})
		}
	}
	observation.byteRanges = keptBytes
	if observation.totalBytes != 0 {
		observation.totalBytes = len(after)
	}
	tracker.seen[absPath] = observation
}

// changedLineSpan reports the 1-based first line that differs between before and
// after, the 1-based last line of BEFORE that differs, and the line-count delta.
//
// Computed from a common prefix and suffix rather than from the caller's
// replacement spans: one edit_file call with replace_all can rewrite many
// scattered occurrences, and the span between the outermost two is the only
// region that is honestly unknown afterwards.
func changedLineSpan(before, after string) (firstChanged, lastChangedBefore, delta int) {
	beforeLines := splitLinesForTracking(before)
	afterLines := splitLinesForTracking(after)
	prefix := 0
	for prefix < len(beforeLines) && prefix < len(afterLines) && beforeLines[prefix] == afterLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(beforeLines)-prefix && suffix < len(afterLines)-prefix &&
		beforeLines[len(beforeLines)-1-suffix] == afterLines[len(afterLines)-1-suffix] {
		suffix++
	}
	return prefix + 1, len(beforeLines) - suffix, len(afterLines) - len(beforeLines)
}

// changedByteSpan is changedLineSpan in bytes: the first differing offset, the
// end offset of the changed region in BEFORE, and the size delta.
func changedByteSpan(before, after []byte) (firstChanged, lastChangedBefore, delta int) {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	return prefix, len(before) - suffix, len(after) - len(before)
}

func splitLinesForTracking(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func countLines(content []byte) int {
	return len(splitLinesForTracking(string(content)))
}

// coversFully reports whether ranges together cover every line in [start, end].
//
// Ranges are merged rather than scanned line by line: a caller asking about a
// large file would otherwise walk every line against every recorded range, and
// the answer is the same.
func coversFully(ranges []lineRange, start int, end int) bool {
	if start > end {
		return true
	}
	if len(ranges) == 0 {
		return false
	}
	sorted := make([]lineRange, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(left int, right int) bool {
		if sorted[left].start != sorted[right].start {
			return sorted[left].start < sorted[right].start
		}
		return sorted[left].end < sorted[right].end
	})
	next := start
	for _, seen := range sorted {
		if seen.start > next {
			return false
		}
		if seen.end >= next {
			next = seen.end + 1
		}
		if next > end {
			return true
		}
	}
	return next > end
}

// coversBytes reports whether half-open byte ranges cover [start, end).
func coversBytes(ranges []lineRange, start, end int) bool {
	if start >= end {
		return true
	}
	if len(ranges) == 0 {
		return false
	}
	sorted := make([]lineRange, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(left, right int) bool {
		if sorted[left].start != sorted[right].start {
			return sorted[left].start < sorted[right].start
		}
		return sorted[left].end < sorted[right].end
	})
	next := start
	for _, seen := range sorted {
		if seen.start > next {
			return false
		}
		if seen.end > next {
			next = seen.end
		}
		if next >= end {
			return true
		}
	}
	return false
}

// SeenRange reports whether every line in the requested range was returned
// exactly to the model for the currently tracked version.
func (tracker *FileTracker) SeenRange(absPath string, start, end int) bool {
	if tracker == nil {
		return true
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	observation, ok := tracker.seen[absPath]
	if !ok {
		return false
	}
	if observation.whole {
		return true
	}
	return coversFully(observation.ranges, start, end)
}

// SeenBytes reports whether every byte in [start, end) was returned exactly to
// the model for the currently tracked version.
func (tracker *FileTracker) SeenBytes(absPath string, start, end int) bool {
	if tracker == nil {
		return true
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	observation, ok := tracker.seen[absPath]
	if !ok {
		return false
	}
	return observation.whole || coversBytes(observation.byteRanges, start, end)
}

func (tracker *FileTracker) SeenWhole(absPath string) bool {
	if tracker == nil {
		return true
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	observation, ok := tracker.seen[absPath]
	if !ok {
		return false
	}
	if observation.whole {
		return true
	}
	// Derived from the ranges rather than tracked as its own flag. The flag was
	// only ever set by a SINGLE read covering the file, so a file read in two
	// halves stayed "not seen whole" forever even though SeenRange agreed every
	// line had been seen — and write_file, which gates on this, then refused the
	// overwrite with advice to read the file that could not change the answer.
	if observation.total > 0 && coversFully(observation.ranges, 1, observation.total) {
		return true
	}
	return observation.totalBytes > 0 && coversBytes(observation.byteRanges, 0, observation.totalBytes)
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

// RecordCreated notes that absPath is a brand-new file a tool just created in
// this session (as opposed to an overwrite of something that already existed).
func (tracker *FileTracker) RecordCreated(absPath string) {
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.createdSeen == nil {
		tracker.createdSeen = make(map[string]bool)
	}
	if tracker.createdSeen[absPath] {
		return
	}
	tracker.createdSeen[absPath] = true
	tracker.created = append(tracker.created, absPath)
}

// CreatedFiles returns the absolute paths of every brand-new file recorded via
// RecordCreated during this session, in first-created order. The caller owns
// the returned slice.
func (tracker *FileTracker) CreatedFiles() []string {
	if tracker == nil {
		return nil
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	out := make([]string, len(tracker.created))
	copy(out, tracker.created)
	return out
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
	delete(tracker.seen, absPath)
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

func fileUnseenMessage(relativePath string) string {
	return "Error writing " + relativePath + ": the intended change depends on content that has not been read exactly in this session. Read the affected lines with read_file using offset/limit (or byte_offset/byte_limit for a very long line), then retry the edit."
}
