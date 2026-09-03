package remote

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/daemon"
	"github.com/Gitlawb/zero/internal/fsutil"
	"github.com/Gitlawb/zero/internal/lockutil"
)

// gitTimeout bounds a single git invocation (bundle create/verify, clone) so a
// hung git process cannot pin a connection or the upload path indefinitely.
const gitTimeout = 2 * time.Minute

// bundleChunkSize is the per-frame payload when streaming a bundle file. It is
// kept comfortably under daemon.MaxFrameSize (1 MiB).
const bundleChunkSize = 512 << 10

// bundleHeader is the first control frame of a bundle upload (after the auth
// handshake): it declares the link id and the exact byte size that follows.
type bundleHeader struct {
	LinkID string `json:"link_id"`
	Size   int64  `json:"size"`
}

// bundleResult is the bridge's reply once the bundle is received and extracted.
type bundleResult struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

func writeBundleHeader(w io.Writer, h bundleHeader) error {
	payload, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return daemon.WriteFrame(w, daemon.KindCtrl, payload)
}

func readBundleHeader(r io.Reader) (bundleHeader, error) {
	kind, payload, err := daemon.ReadFrame(r)
	if err != nil {
		return bundleHeader{}, err
	}
	if kind != daemon.KindCtrl {
		return bundleHeader{}, errors.New("remote: expected bundle header frame")
	}
	var h bundleHeader
	if err := json.Unmarshal(payload, &h); err != nil {
		return bundleHeader{}, fmt.Errorf("remote: decode bundle header: %w", err)
	}
	return h, nil
}

func writeBundleResult(w io.Writer, res bundleResult) error {
	payload, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return daemon.WriteFrame(w, daemon.KindCtrl, payload)
}

func readBundleResult(r io.Reader) (bundleResult, error) {
	kind, payload, err := daemon.ReadFrame(r)
	if err != nil {
		return bundleResult{}, err
	}
	if kind != daemon.KindCtrl {
		return bundleResult{}, errors.New("remote: expected bundle result frame")
	}
	var res bundleResult
	if err := json.Unmarshal(payload, &res); err != nil {
		return bundleResult{}, fmt.Errorf("remote: decode bundle result: %w", err)
	}
	return res, nil
}

// ---- server side -----------------------------------------------------------

// handleBundle receives an uploaded bundle, extracts it into a per-link working
// tree, and reports the outcome. It always closes conn.
func (b *Bridge) handleBundle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	res := b.receiveBundle(conn)
	if !res.OK {
		b.logf("remote: bundle upload rejected: %s", res.Message)
	}
	_ = writeBundleResult(conn, res)
}

// receiveBundle reads the header + framed bundle bytes, verifies the bundle, and
// extracts it under bundleDir. Every failure returns a non-OK result rather than
// panicking, and the staged temp file is always removed.
func (b *Bridge) receiveBundle(conn net.Conn) bundleResult {
	hdr, err := readBundleHeader(conn)
	if err != nil {
		return bundleResult{Message: "read bundle header: " + err.Error()}
	}
	id, err := sanitizeLinkID(hdr.LinkID)
	if err != nil {
		return bundleResult{Message: err.Error()}
	}
	if hdr.Size <= 0 || hdr.Size > b.maxBundleBytes {
		return bundleResult{Message: fmt.Sprintf("invalid bundle size %d (max %d)", hdr.Size, b.maxBundleBytes)}
	}

	tmp, err := os.CreateTemp("", "zero-remote-*.bundle")
	if err != nil {
		return bundleResult{Message: "stage bundle: " + err.Error()}
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := streamFramesToFile(conn, tmp, hdr.Size); err != nil {
		_ = tmp.Close()
		return bundleResult{Message: "receive bundle: " + err.Error()}
	}
	if err := tmp.Close(); err != nil {
		return bundleResult{Message: "stage bundle: " + err.Error()}
	}

	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), gitTimeout)
	defer cancelVerify()
	if err := gitBundleVerify(verifyCtx, tmpName); err != nil {
		return bundleResult{Message: "bundle verify: " + err.Error()}
	}
	dest := filepath.Join(b.bundleDir, id)
	if !withinDir(b.bundleDir, dest) {
		return bundleResult{Message: "invalid link id"}
	}
	// extractBundle starts the clone's own gitTimeout once it holds the lock for
	// dest, so an upload queued behind another does not spend that budget waiting.
	if err := extractBundle(context.Background(), tmpName, dest, b.logf); err != nil {
		return bundleResult{Message: "extract bundle: " + err.Error()}
	}
	return bundleResult{OK: true, Path: dest}
}

// streamFramesToFile copies exactly size bytes from KindData frames on r into w.
// A non-data frame, or any frame that would overrun the declared size, is an
// error (fail closed) so a peer cannot write past the cap.
func streamFramesToFile(r io.Reader, w io.Writer, size int64) error {
	remaining := size
	for remaining > 0 {
		kind, payload, err := daemon.ReadFrame(r)
		if err != nil {
			return err
		}
		if kind != daemon.KindData {
			return errors.New("expected bundle data frame")
		}
		if int64(len(payload)) > remaining {
			return errors.New("bundle exceeds declared size")
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
		remaining -= int64(len(payload))
	}
	return nil
}

// stagingPrefix names the per-extract staging directories created beside dest.
// sanitizeLinkID refuses every dot-prefixed id so a link can never name one.
const stagingPrefix = ".staging-"

// keptPrefix names a staged backup that recovery decided to keep rather than
// order against a tree it had just put back. Recovery runs again on every start,
// and by then it can no longer tell that tree from one a later extract
// published, so the copy is parked under a name the scan does not enumerate.
// sanitizeLinkID refuses every dot-prefixed id, so this can never take a link's
// name.
const keptPrefix = ".kept-"

// lockDirName holds the per-link advisory lock files that serialize extracts
// across processes. Dot-prefixed for the same reason stagingPrefix is.
const lockDirName = ".extract-locks"

// stagingMarkerFile records, inside a staging dir, the transaction that created
// it: its kind, the link it is for, and the sequence in its own name. Without it
// a crash leaves an orphan nothing can attribute, and recovery has to retain a
// copy it can never name.
const stagingMarkerFile = "txn"

// committedFile records, inside a staging dir, that the publish rename landed.
// It is the only evidence that the copy in backup was superseded, so recovery
// has to keep a backup that carries no flag.
const committedFile = "committed"

// txnKindBundleExtract is the marker kind extractBundle writes. A marker naming
// another kind belongs to another site's transaction and is not this code's to
// act on.
const txnKindBundleExtract = "bundle-extract"

// extractLockPoll is how often a cross-process extract lock is retried.
const extractLockPoll = 50 * time.Millisecond

// fsOps is the filesystem seam the write path, the allocator, and recovery take
// every step through. A step that calls os directly cannot be made to fail, and
// the crash each of these steps exists to survive is then only reasoned about.
// Every field is one call, so a test can fail the second remove or the rename
// whose source is one particular backup and leave the rest of the pass real.
type fsOps struct {
	rename     func(from, to string) error
	removeAll  func(path string) error
	stat       func(name string) (os.FileInfo, error)
	lstat      func(name string) (os.FileInfo, error)
	readDir    func(name string) ([]os.DirEntry, error)
	readFile   func(name string) ([]byte, error)
	mkdir      func(name string, perm os.FileMode) error
	writeFile  func(name string, data []byte, perm os.FileMode) error
	create     func(name string, flag int, perm os.FileMode) (*os.File, error)
	createTemp func(dir, pattern string) (*os.File, error)
}

// stagingFS is the seam every filesystem step in this file goes through. Tests
// swap a field and restore it; nothing else writes to it.
var stagingFS = fsOps{
	rename:     os.Rename,
	removeAll:  os.RemoveAll,
	stat:       os.Stat,
	lstat:      os.Lstat,
	readDir:    os.ReadDir,
	readFile:   os.ReadFile,
	mkdir:      os.Mkdir,
	writeFile:  os.WriteFile,
	create:     os.OpenFile,
	createTemp: os.CreateTemp,
}

// extractLocks serializes extracts per destination. Each bundle upload runs in
// its own connection goroutine, so two uploads of one link id would otherwise
// interleave their swap steps and clobber each other.
var extractLocks = struct {
	mu    sync.Mutex
	locks map[string]*extractLock
}{locks: map[string]*extractLock{}}

type extractLock struct {
	mu   sync.Mutex
	refs int
}

// lockExtract blocks until dest is free and returns its release func. Entries
// are refcounted so the map cannot grow with every link id ever uploaded.
func lockExtract(dest string) func() {
	extractLocks.mu.Lock()
	entry := extractLocks.locks[dest]
	if entry == nil {
		entry = &extractLock{}
		extractLocks.locks[dest] = entry
	}
	entry.refs++
	extractLocks.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		extractLocks.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(extractLocks.locks, dest)
		}
		extractLocks.mu.Unlock()
	}
}

// lockExtractFile takes the cross-process advisory lock for dest, waiting until
// ctx is done or the wait budget runs out. The in-process lock already excludes
// this daemon's own goroutines; this excludes a second daemon sharing the dir.
func lockExtractFile(ctx context.Context, bundleDir, dest string) (func(), error) {
	deadline := time.NewTimer(gitTimeout)
	defer deadline.Stop()
	for {
		release, held, err := tryLockExtractFile(bundleDir, dest)
		if err != nil {
			return nil, err
		}
		if !held {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("remote: timed out waiting for the extract lock on %s", dest)
		case <-time.After(extractLockPoll):
		}
	}
}

// tryLockExtractFile takes the per-link advisory lock without waiting. It
// reports held when a live extract owns the link, which is never an error: the
// caller either waits or leaves that link alone.
func tryLockExtractFile(bundleDir, dest string) (release func(), held bool, err error) {
	lockDir := filepath.Join(bundleDir, lockDirName)
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, false, err
	}
	lock, err := lockutil.TryAcquireFileLockAt(bundleDir, filepath.Join(lockDir, filepath.Base(dest)+".lock"))
	if err != nil {
		if errors.Is(err, lockutil.ErrLockHeld) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() { _ = lock.Release() }, false, nil
}

// recoverBundleDir repairs what a crash left behind in dir. A staging dir whose
// backup belongs to a link with no live tree is put back; one that no extract
// can still own is removed. It is called once at bridge construction, before any
// upload is served, and never removes a staging dir a live extract may hold.
func recoverBundleDir(dir string, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	entries, err := stagingFS.readDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			logf("remote: could not scan bundle dir %s: %v", dir, err)
		}
		return
	}
	// One link can have several staged backups: a cleanup that could not finish
	// leaves one behind, and a later crash adds another. Newest first, so the
	// tree that comes back is the most recent one rather than whichever the
	// directory happened to list first. The order comes from the sequence the
	// extract allocated against the entries already in the directory, not from a
	// wall clock and not from a directory mtime. A clock can move backward and
	// invert two stamps; an extract that read an existing entry always numbers
	// above it. Mtimes stay rejected for their own reasons: they track a tree's
	// contents, and a coarse filesystem gives two of them the same value anyway.
	staged := make([]stagedExtract, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), stagingPrefix) {
			continue
		}
		stamp, stamped := stagingStamp(entry.Name())
		staged = append(staged, stagedExtract{path: filepath.Join(dir, entry.Name()), stamp: stamp, stamped: stamped})
	}
	slices.SortStableFunc(staged, func(a, b stagedExtract) int {
		if a.stamped != b.stamped {
			if a.stamped {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.stamp, a.stamp)
	})

	// Which links this pass put a tree back on, and the staging it came from.
	restored := map[string]stagedExtract{}
	for _, s := range staged {
		staging := s.path
		if restoreStagedBackup(dir, s, restored, logf) {
			continue
		}
		// No backup to attribute. A dir with a .git at its root is not staging at
		// all: link ids starting with '.' used to be accepted, so this may be a
		// work tree someone published under a name that now looks reserved.
		// Never reap that.
		if _, err := stagingFS.stat(filepath.Join(staging, ".git")); err == nil {
			logf("remote: %s holds a work tree, not a staged extract; leaving it in place", staging)
			continue
		}
		// Only reap once no clone can still be running: gitTimeout bounds a
		// clone, so anything older than that is abandoned.
		info, err := stagingFS.stat(staging)
		if err != nil || time.Since(info.ModTime()) < 2*gitTimeout {
			continue
		}
		if err := stagingFS.removeAll(staging); err != nil {
			logf("remote: could not remove abandoned staging dir %s: %v", staging, err)
		}
	}
}

// stagedExtract is a staging dir plus the creation order recorded in its name.
// stamped is false for a name this package did not write, which is an ordering
// it must not claim to know.
type stagedExtract struct {
	path    string
	stamp   int64
	stamped bool
}

// stagingStamp reads back the per-directory sequence createSequencedStagingDir
// put in a staging name. The grammar is exact: the prefix, stagingSeqDigits
// ASCII digits, the suffix, nothing else. No released version wrote a stamped
// name at all, so a looser parse buys no migration and costs ownership: v0.8.0
// staged under os.MkdirTemp(parent, ".staging-*"), whose decimal suffix carries
// no second '-', and dot-prefixed link ids were once accepted, so a published
// work tree can sit under any name at all. One that reads as a sequence gets a
// say in the ordering, and one at the maximum stops the allocator for every link
// in the directory.
func stagingStamp(name string) (int64, bool) {
	digits, ok := strings.CutPrefix(name, stagingPrefix)
	if !ok {
		return 0, false
	}
	digits, ok = strings.CutSuffix(digits, stagingSeqSuffix)
	if !ok || len(digits) != stagingSeqDigits {
		return 0, false
	}
	for i := 0; i < len(digits); i++ {
		// ParseInt accepts a leading sign, and a link id may contain '-', so
		// without this a legacy name reads back as a negative sequence the
		// writer could never have emitted.
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	stamp, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		// Digits alone can still name a number past int64.
		return 0, false
	}
	return stamp, true
}

// stagingSeqSuffix closes a sequenced staging name, and stagingSeqDigits is the
// width createSequencedStagingDir's %020d writes. The parser requires both
// exactly, and createSequencedStagingDir reads its own name back before using
// it, so a format string that drifted from either would fail there rather than
// putting a name on disk that reads as unstamped: an unstamped entry sorts last
// and is always retained, so the ordering key would simply stop existing with
// nothing failing.
const (
	stagingSeqSuffix = "-seq"
	stagingSeqDigits = 20
)

// stagingSeqAttempts bounds the walk up from a taken number, in the spirit of
// the retry limit os.MkdirTemp applies to its own random names.
const stagingSeqAttempts = 10000

// nextStagingSeq is the number a new staging dir should claim: one past the
// highest already in the directory. That is what makes the order survive a
// clock that moves backward. An extract that reads an existing entry always
// allocates above it, and no clock correction can invert that, whereas the
// wall-clock stamp this replaces inverted whenever the clock did.
//
// Kept backups count. parkKeptBackup derives the parked name from the staging
// name, so a number handed out twice makes the second park land on an occupied
// name; that rename refuses, the backup stays under stagingPrefix, and the next
// pass deletes it as superseded. Counting kept names keeps a parked number out
// of circulation. Recovery still does not enumerate them.
func nextStagingSeq(dir string) (int64, error) {
	entries, err := stagingFS.readDir(dir)
	if err != nil {
		return 0, err
	}
	var high int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Read only the names this package writes. The sibling directories here
		// are the per-link work trees, and a link id is whatever the uploading
		// client sent; stagingStamp trims its prefix with TrimPrefix, which is a
		// no-op on a name that lacks it, so an unfiltered scan would read a link
		// named "2024-project" as sequence 2024 and one named for int64's
		// maximum as a permanent refusal to allocate anything.
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, keptPrefix):
			name = stagingPrefix + strings.TrimPrefix(name, keptPrefix)
		case strings.HasPrefix(name, stagingPrefix):
		default:
			continue
		}
		stamp, ok := stagingStamp(name)
		if !ok || stamp <= high {
			continue
		}
		// A dir with a .git at its own root is a work tree, not something this
		// package wrote: link ids starting with '.' used to be accepted, so one
		// can carry the exact generated name. The clone and the set-aside tree a
		// live staging dir holds are one level down, in repo/ and backup/, so
		// this vetoes the legacy tree without releasing a number that is still
		// spoken for.
		if _, err := stagingFS.stat(filepath.Join(dir, entry.Name(), ".git")); err == nil {
			continue
		}
		high = stamp
	}
	if high == math.MaxInt64 {
		// The addition below would wrap negative, and %020d of a negative
		// renders a '-' the parser reads as empty digits, so the entry would
		// drop out of the ordering without saying so. Refuse instead.
		return 0, fmt.Errorf("remote: %s holds a staging name at the maximum sequence; remove it before extracting again", dir)
	}
	return high + 1, nil
}

// createSequencedStagingDir claims the first free name from n upward. Exclusive
// creation is what arbitrates: two extracts racing in one directory cannot both
// win a name, and the loser walks up to a number strictly above the winner's.
func createSequencedStagingDir(dir string, n int64) (string, error) {
	if n < 1 {
		return "", fmt.Errorf("remote: refusing to allocate staging sequence %d", n)
	}
	for i := 0; i < stagingSeqAttempts; i++ {
		name := fmt.Sprintf("%s%020d%s", stagingPrefix, n, stagingSeqSuffix)
		// The writer and the reader agree on the format or nothing is written.
		// Checking here rather than trusting the format string is what keeps a
		// silently unstamped name from reaching disk.
		if stamp, ok := stagingStamp(name); !ok || stamp != n {
			return "", fmt.Errorf("remote: staging name %q does not read back as sequence %d", name, n)
		}
		path := filepath.Join(dir, name)
		err := stagingFS.mkdir(path, 0o700)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		if n == math.MaxInt64 {
			return "", fmt.Errorf("remote: staging sequence exhausted in %s", dir)
		}
		n++
	}
	return "", fmt.Errorf("remote: could not claim a staging name in %s after %d attempts", dir, stagingSeqAttempts)
}

// restoreStagedBackup puts a staged backup back if its link has no live tree.
// It reports whether staging was dealt with and needs no further handling.
// restored carries the links this recovery pass has already put a tree back on.
func restoreStagedBackup(dir string, s stagedExtract, restored map[string]stagedExtract, logf func(string, ...any)) bool {
	staging := s.path
	backup := filepath.Join(staging, "backup")
	if _, err := stagingFS.stat(backup); err != nil {
		return false
	}
	m, err := readMarker(staging)
	if err != nil {
		logf("remote: staged tree in %s has no usable transaction marker (%v); leaving it in place", staging, err)
		return true
	}
	id, err := sanitizeLinkID(m.Dest)
	if err != nil {
		logf("remote: staged tree in %s names an invalid link (%v); leaving it in place", staging, err)
		return true
	}
	dest := filepath.Join(dir, id)
	if !withinDir(dir, dest) {
		logf("remote: staged tree in %s names a link outside the bundle dir; leaving it in place", staging)
		return true
	}
	// A live extract mid-swap looks exactly like a crashed one: its backup is
	// aside and dest is briefly absent. Only the lock tells them apart, so skip
	// any link something still owns rather than taking its tree.
	release, held, err := tryLockExtractFile(dir, dest)
	if err != nil {
		logf("remote: could not lock %s while recovering %s: %v", id, staging, err)
		return true
	}
	if held {
		return true
	}
	defer release()
	if _, err := stagingFS.stat(dest); err == nil {
		// The link already has a tree, and a backup only ever holds the tree that
		// was live BEFORE it: backup is filled by renaming dest aside, so dest
		// holding anything at all means a later extract published over it. That
		// is what makes the backup superseded -- not a timestamp comparison,
		// which two directories can tie on and which tracks a tree's contents
		// rather than when it was promoted.
		if from, ours := restored[dest]; ours && (!s.stamped || !from.stamped || s.stamp >= from.stamp) {
			// dest is a tree THIS pass put back, so nothing published over this
			// backup and the reasoning above does not apply. Without an order
			// both names agree on, which of the two is current is unknown.
			logf("remote: staged tree in %s cannot be ordered against the tree just restored for %s; keeping it", staging, id)
			parkKeptBackup(dir, staging, id, logf)
			return true
		}
		// The upload that published dest reported success to its client while
		// this whole copy of the prior tree was still on disk, and
		// extractBundle's own cleanup failure is only logged. Say what is being
		// reclaimed, so a bridge that ran without a logger configured is not the
		// difference between the space being accounted for and not.
		logf("remote: reclaiming the staged tree in %s that %s's live tree superseded", staging, id)
		if err := stagingFS.removeAll(staging); err != nil {
			logf("remote: could not remove superseded staging dir %s: %v", staging, err)
		}
		return true
	}
	if err := stagingFS.rename(backup, dest); err != nil {
		logf("remote: could not restore the staged tree for %s from %s: %v", id, staging, err)
		return true
	}
	restored[dest] = s
	logf("remote: restored the work tree for %s from %s after an interrupted extract", id, staging)
	if err := stagingFS.removeAll(staging); err != nil {
		logf("remote: could not remove staging dir %s after restoring %s: %v", staging, id, err)
	}
	return true
}

// parkKeptBackup moves a staging dir out of the prefix recovery scans, so the
// next pass leaves alone what this one deliberately kept instead of reading the
// restored tree at dest as a later extract publishing over it. A rename onto an
// existing directory fails rather than replacing it, so a legacy link that
// happens to carry the parked name is never clobbered; the copy just stays where
// it is, which is the same fail-safe one pass later.
func parkKeptBackup(dir, staging, id string, logf func(string, ...any)) {
	parked := filepath.Join(dir, keptPrefix+strings.TrimPrefix(filepath.Base(staging), stagingPrefix))
	if err := stagingFS.rename(staging, parked); err != nil {
		logf("remote: could not park the kept backup for %s at %s: %v", id, parked, err)
	}
}

// txnMarker is what a staging dir carries to prove which transaction created it.
// Seq repeats the number in the directory's own name: a marker that disagrees
// with its name proves nothing about who wrote either, so the pair is what makes
// the directory owned rather than the name alone.
type txnMarker struct {
	Kind string `json:"kind"`
	Dest string `json:"dest"`
	Seq  int64  `json:"seq"`
}

// errMarkerMissing reports that a staging dir carries no marker at all, which is
// a different fact from a marker that could not be read: the first is a crash
// before the marker was written, the second is a filesystem fault. Deciding
// between them off a bare error would fold a fault into "not ours".
var errMarkerMissing = errors.New("remote: staging dir carries no transaction marker")

// writeMarker publishes m into dir under stagingMarkerFile, through a temp file
// in that same directory so the rename is the only step a reader can observe. A
// plain write can be torn by a crash, and a half-written marker parses as
// nothing while still occupying the name recovery reads to decide ownership.
func writeMarker(dir string, m txnMarker) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := stagingFS.createTemp(dir, stagingMarkerFile+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// Sync before the rename: the rename can reach disk ahead of the bytes it
	// names, which is exactly the partial marker the temp file exists to avoid.
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = stagingFS.removeAll(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = stagingFS.removeAll(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = stagingFS.removeAll(name)
		return err
	}
	if err := fsutil.RenameWithRetry(name, filepath.Join(dir, stagingMarkerFile), stagingFS.rename); err != nil {
		_ = stagingFS.removeAll(name)
		return err
	}
	return nil
}

// readMarker reads back what writeMarker published. A dir with no marker yields
// errMarkerMissing; every other failure is wrapped, so a caller can tell "this
// was never ours" from "this could not be read", which are opposite decisions.
func readMarker(dir string) (txnMarker, error) {
	raw, err := stagingFS.readFile(filepath.Join(dir, stagingMarkerFile))
	if err != nil {
		if os.IsNotExist(err) {
			return txnMarker{}, errMarkerMissing
		}
		return txnMarker{}, fmt.Errorf("read transaction marker in %s: %w", dir, err)
	}
	var m txnMarker
	if err := json.Unmarshal(raw, &m); err != nil {
		return txnMarker{}, fmt.Errorf("parse transaction marker in %s: %w", dir, err)
	}
	return m, nil
}

// extractBundle clones bundleFile into a staging dir beside dest, then swaps the
// clone into place (replacing any prior extraction for this link id). git clone
// needs a non-existent target, hence the staging dir. The steps run in this
// order: allocate the sequenced staging dir, write its transaction marker, clone
// into it, set the live tree aside, publish the clone, record the commit flag,
// remove the staging dir. The marker precedes the first destructive rename and
// the flag follows the publish, so every copy this leaves on disk names the
// transaction that wrote it and says whether that transaction committed. The
// live tree is moved aside rather than deleted and is put back if the publish
// fails, so on every error return dest holds either the prior extraction or the
// new one, never neither. Swapping a directory is two renames and cannot be made
// atomic, so a crash between them leaves dest absent with the prior tree in
// staging/backup, which the marker is what lets recovery attribute and put back.
// logf may be nil.
func extractBundle(ctx context.Context, bundleFile, dest string, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	unlock := lockExtract(dest)
	defer unlock()
	unlockFile, err := lockExtractFile(ctx, parent, dest)
	if err != nil {
		return err
	}
	defer unlockFile()

	// The sequence records this extract's place in the order, which is what lets
	// recovery tell an older leftover staging dir from a newer one. It is one
	// past the highest number already in the directory, claimed by exclusive
	// creation, so a concurrent extract for another link cannot take the same
	// value and a clock that moves backward cannot invert the order. Every number
	// on disk is one of these per-directory sequences: no released version wrote
	// a stamped name at all (v0.8.0 staged under os.MkdirTemp's random suffix),
	// so nothing here is ordering against a wall clock it inherited.
	seq, err := nextStagingSeq(parent)
	if err != nil {
		return err
	}
	staging, err := createSequencedStagingDir(parent, seq)
	if err != nil {
		return err
	}
	// Staging also holds the prior tree while the swap is in flight, so it is
	// only cleaned up once dest is known to hold one of the two trees.
	cleanupStaging := true
	defer func() {
		if !cleanupStaging {
			return
		}
		// A failure here strands a whole copy of the prior tree under a
		// dot-prefixed dir nothing else enumerates, so say so rather than
		// leaking it silently.
		if err := stagingFS.removeAll(staging); err != nil {
			logf("remote: could not remove bundle staging dir %s: %v", staging, err)
		}
	}()
	// Attribute the staging dir before anything else touches the filesystem: from
	// here on every failure can leave a copy of a tree behind, and a copy no
	// marker names is one recovery can neither put back nor ever reclaim. The
	// name is the authority on the sequence, because createSequencedStagingDir
	// walks up from seq when a name is taken, and a marker that disagrees with
	// its own directory name proves nothing.
	claimed, _ := stagingStamp(filepath.Base(staging))
	if err := writeMarker(staging, txnMarker{Kind: txnKindBundleExtract, Dest: filepath.Base(dest), Seq: claimed}); err != nil {
		return err
	}
	cloneCtx, cancelClone := context.WithTimeout(ctx, gitTimeout)
	defer cancelClone()
	cloneDest := filepath.Join(staging, "repo")
	if err := gitClone(cloneCtx, bundleFile, cloneDest); err != nil {
		return err
	}

	// Every rename stays inside parent, so none of them crosses a filesystem.
	backup := filepath.Join(staging, "backup")
	restore := func() error { return nil }
	if err := fsutil.RenameWithRetry(dest, backup, stagingFS.rename); err == nil {
		restore = func() error { return fsutil.RenameWithRetry(backup, dest, stagingFS.rename) }
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := fsutil.RenameWithRetry(cloneDest, dest, stagingFS.rename); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			// dest is empty and the only copy of the prior tree is the backup,
			// so keep staging rather than deleting the tree on the way out.
			cleanupStaging = false
			return fmt.Errorf("publish extraction: %w (prior tree left in %s: %v)", err, backup, restoreErr)
		}
		return err
	}
	// The flag is the only evidence that the copy in backup was published over.
	// Without it that copy has to be kept, so a failure here costs one retained
	// tree and never the publish, which has already landed and is reported as
	// the success it is.
	flag, err := stagingFS.create(filepath.Join(staging, committedFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanupStaging = false
		logf("remote: published %s but could not record the commit flag in %s: %v", dest, staging, err)
		return nil
	}
	_ = flag.Close()
	return nil
}

// ---- client side -----------------------------------------------------------

// UploadRepoBundle creates a git bundle of repoDir's full history and uploads it
// to the remote bridge over an authenticated, bundle-mode TLS connection. The
// bridge extracts it into a per-link working tree and returns its path, captured
// in the returned SessionLink. repoDir must be a git work tree.
func UploadRepoBundle(cfg RemoteConfig, repoDir, linkID string) (*SessionLink, error) {
	id, err := sanitizeLinkID(linkID)
	if err != nil {
		return nil, err
	}
	repoDir = strings.TrimSpace(repoDir)
	if repoDir == "" {
		return nil, errors.New("remote: repo dir is required")
	}
	if !isGitWorktree(repoDir) {
		return nil, fmt.Errorf("remote: %s is not a git repository", repoDir)
	}

	// Reserve a unique temp name, then let git create the bundle fresh at it.
	tmp, err := os.CreateTemp("", "zero-bundle-*.bundle")
	if err != nil {
		return nil, fmt.Errorf("remote: stage bundle: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpName)
	defer func() { _ = os.Remove(tmpName) }()

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if err := gitBundleCreate(ctx, repoDir, tmpName); err != nil {
		return nil, fmt.Errorf("remote: create bundle: %w", err)
	}
	sum, size, err := hashFile(tmpName)
	if err != nil {
		return nil, fmt.Errorf("remote: hash bundle: %w", err)
	}

	conn, err := dialAuthenticated(cfg, ModeBundle)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	if err := writeBundleHeader(conn, bundleHeader{LinkID: id, Size: size}); err != nil {
		return nil, fmt.Errorf("remote: send bundle header: %w", err)
	}
	if err := streamFileFrames(conn, tmpName); err != nil {
		return nil, fmt.Errorf("remote: send bundle: %w", err)
	}
	res, err := readBundleResult(conn)
	if err != nil {
		return nil, fmt.Errorf("remote: bundle result: %w", err)
	}
	if !res.OK {
		return nil, fmt.Errorf("remote: bundle rejected: %s", res.Message)
	}

	return &SessionLink{
		Address:      strings.TrimSpace(cfg.Address),
		ServerName:   strings.TrimSpace(cfg.ServerName),
		CACertFile:   strings.TrimSpace(cfg.CACertFile),
		LinkID:       id,
		RemotePath:   res.Path,
		BundleSHA256: sum,
	}, nil
}

// streamFileFrames writes the file at path to w as a sequence of KindData frames.
func streamFileFrames(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, bundleChunkSize)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if werr := daemon.WriteFrame(w, daemon.KindData, buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ---- git + path helpers ----------------------------------------------------

func gitBundleCreate(ctx context.Context, repoDir, outFile string) error {
	return runGit(ctx, repoDir, "bundle", "create", outFile, "--all")
}

func gitBundleVerify(ctx context.Context, bundleFile string) error {
	return runGit(ctx, "", "bundle", "verify", bundleFile)
}

func gitClone(ctx context.Context, bundleFile, destDir string) error {
	return runGit(ctx, "", "clone", "--quiet", bundleFile, destDir)
}

// isGitWorktree reports whether dir is inside a git work tree.
func isGitWorktree(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// runGit runs a git subcommand, returning a concise single-line error on failure.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := firstLine(strings.TrimSpace(string(out)))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}

// sanitizeLinkID validates a link id used as a single path component under the
// bundle dir. It allows letters, digits, '-', '_', '.', forbids a leading '.',
// and caps the length, so an id can never escape the bundle dir and can never
// name one of the stagingPrefix directories an extract creates beside it.
func sanitizeLinkID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("remote: link id is required")
	}
	if len(id) > 128 {
		return "", errors.New("remote: link id too long (max 128)")
	}
	if strings.HasPrefix(id, ".") {
		return "", errors.New("remote: link id may not start with '.'")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return "", errors.New("remote: link id may only contain letters, digits, '-', '_', '.'")
		}
	}
	return id, nil
}

// withinDir reports whether target resolves to a path inside root.
func withinDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// hashFile returns the hex SHA-256 and byte size of the file at path.
func hashFile(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
