package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/daemon"
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
const stagingPrefix = ".staging-"

// renameDir moves a directory into its published location. It is a var so tests
// can force a failure at the steps whose errors would otherwise be unrecoverable.
var renameDir = os.Rename

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

// extractBundle clones bundleFile into a staging dir beside dest, then swaps the
// clone into place (replacing any prior extraction for this link id). git clone
// needs a non-existent target, hence the staging dir. The live tree is moved
// aside rather than deleted and is put back if the publish fails, so on every
// error return dest holds either the prior extraction or the new one, never
// neither. Swapping a directory is two renames and cannot be made atomic, so a
// crash between them leaves dest absent with the prior tree in staging/backup;
// nothing reaps that on restart. logf may be nil.
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

	staging, err := os.MkdirTemp(parent, stagingPrefix+"*")
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
		if err := os.RemoveAll(staging); err != nil {
			logf("remote: could not remove bundle staging dir %s: %v", staging, err)
		}
	}()
	cloneCtx, cancelClone := context.WithTimeout(ctx, gitTimeout)
	defer cancelClone()
	cloneDest := filepath.Join(staging, "repo")
	if err := gitClone(cloneCtx, bundleFile, cloneDest); err != nil {
		return err
	}

	// Every rename stays inside parent, so none of them crosses a filesystem.
	backup := filepath.Join(staging, "backup")
	restore := func() error { return nil }
	if err := os.Rename(dest, backup); err == nil {
		restore = func() error { return renameDir(backup, dest) }
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := renameDir(cloneDest, dest); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			// dest is empty and the only copy of the prior tree is the backup,
			// so keep staging rather than deleting the tree on the way out.
			cleanupStaging = false
			return fmt.Errorf("publish extraction: %w (prior tree left in %s: %v)", err, backup, restoreErr)
		}
		return err
	}
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
// bundle dir. It allows letters, digits, '-', '_', '.', forbids the traversal
// names, and caps the length — so it can never escape the bundle dir.
func sanitizeLinkID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("remote: link id is required")
	}
	if len(id) > 128 {
		return "", errors.New("remote: link id too long (max 128)")
	}
	if id == "." || id == ".." {
		return "", errors.New("remote: invalid link id")
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
