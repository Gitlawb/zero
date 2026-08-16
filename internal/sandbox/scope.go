package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Scope is the shared set of directories the sandbox allows writes in: the
// workspace root plus zero or more user-granted extra roots. One instance is
// created per run and shared by the policy engine, the OS command runners, and
// the file tools, so a mid-session Add is immediately visible to every layer.
type Scope struct {
	mu            sync.RWMutex
	workspaceRoot string
	readRoots     []string
	extraRoots    []string
	// tempReads / tempWrites count how many LIVE temporary grants depend on a
	// root, so one holder's cleanup cannot revoke another's access.
	//
	// Without them a temporary grant was add-then-remove with no notion of who
	// still needed it: the second caller to ask for a root already present got a
	// NO-OP undo, and the first caller's cleanup removed the root out from under
	// it. Two read-only tools in the same parallel batch, both blocked on the
	// same directory, is exactly that shape — and read-only tools are precisely
	// the ones the batch runs concurrently.
	tempReads  map[string]int
	tempWrites map[string]int
}

// NewScope builds a scope for workspaceRoot plus the given extra roots. The
// workspace root is normalized best-effort (it may not exist in tests); every
// extra root must normalize strictly via Add and an invalid one fails the
// whole construction so a bad --add-dir/config entry surfaces at startup.
func NewScope(workspaceRoot string, extras []string) (*Scope, error) {
	scope := &Scope{workspaceRoot: normalizeWorkspaceRootBestEffort(workspaceRoot)}
	for _, extra := range extras {
		if _, err := scope.Add(extra); err != nil {
			return nil, fmt.Errorf("write root %q: %w", extra, err)
		}
	}
	if scope.workspaceRoot != "" {
		for _, root := range defaultTempWriteRootCandidates() {
			_, _ = scope.Add(root)
		}
	}
	return scope, nil
}

// WorkspaceRoot returns the resolved workspace root. It is safe to call
// without acquiring the lock because workspaceRoot is immutable after
// construction.
func (s *Scope) WorkspaceRoot() string {
	return s.workspaceRoot
}

// Roots returns the workspace root first, then the extra roots, as a copy.
// ExtraRoots returns ONLY the roots granted beyond the workspace, as a copy.
//
// Roots() includes the workspace root as well, which is right for a caller
// asking "everything this run may write". It is wrong for a caller asking "what
// does this run hold BEYOND its workspace" — and one such caller launches child
// agents in an isolated worktree, where handing back the parent's workspace root
// re-opens the very tree the worktree exists to protect.
func (s *Scope) ExtraRoots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.extraRoots...)
}

func (s *Scope) Roots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roots := make([]string, 0, 1+len(s.extraRoots))
	roots = append(roots, s.workspaceRoot)
	roots = append(roots, s.extraRoots...)
	return roots
}

// ReadRoots returns the workspace root, write roots, and read-only roots as a
// copy. Write roots are included because anything writable must also be readable
// by the tool layer and native sandbox profile.
func (s *Scope) ReadRoots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roots := make([]string, 0, 1+len(s.extraRoots)+len(s.readRoots))
	roots = append(roots, s.workspaceRoot)
	roots = append(roots, s.extraRoots...)
	roots = append(roots, s.readRoots...)
	return dedupeScopeRoots(roots)
}

// ExtraReadRoots returns every path the run may READ BEYOND its workspace — the
// write grants AND the read-only grants, deduped, as a copy. It is ReadRoots()
// without the workspace root.
//
// It exists for the same caller ExtraRoots() does: a child agent dispatched into
// its own workspace, which the parent must be able to hand its beyond-workspace
// access without also handing back the parent workspace root (see ExtraRoots).
// ExtraRoots() alone is not enough for that child, because a read grant from
// request_permissions lands in readRoots, not extraRoots — so a child given only
// ExtraRoots() can read what the parent may WRITE beyond its workspace but not
// what it was granted to READ, and a read-only audit of a granted path fails
// "outside the workspace" for want of exactly this list.
func (s *Scope) ExtraReadRoots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roots := make([]string, 0, len(s.extraRoots)+len(s.readRoots))
	roots = append(roots, s.extraRoots...)
	roots = append(roots, s.readRoots...)
	return dedupeScopeRoots(roots)
}

// Add grants write access under path. The path must be an existing directory;
// it is home-expanded, made absolute, and symlink-resolved before being
// trusted, and the filesystem root is rejected outright. Adding a path already
// covered by an existing root is an idempotent success.
func (s *Scope) Add(path string) (string, error) {
	root, err := normalizeScopeRoot(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range append([]string{s.workspaceRoot}, s.extraRoots...) {
		if pathWithinRoot(existing, root) {
			return root, nil
		}
	}
	s.extraRoots = append(s.extraRoots, root)
	return root, nil
}

// AddRead grants read-only access under path. If the path is already covered by
// a writable root, no separate read root is stored.
func (s *Scope) AddRead(path string) (string, error) {
	root, err := normalizeScopeRoot(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeRootCoversLocked(root) {
		return root, nil
	}
	for _, existing := range s.readRoots {
		if pathWithinRoot(existing, root) {
			return root, nil
		}
	}
	s.readRoots = append(s.readRoots, root)
	return root, nil
}

func (s *Scope) AddTemporaryRead(path string) (string, func(), error) {
	root, err := normalizeScopeRoot(path)
	if err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeRootCoversLocked(root) {
		// Covered by a write root — which is NOT necessarily permanent.
		// writeRootCoversLocked scans extraRoots, and AddTemporaryWrite appends
		// TEMPORARY write roots there, so the covering grant may belong to
		// another holder who is going to release it. Take a reference in that
		// case, exactly as the read-covered-by-read path below does: without one
		// the write holder's cleanup silently revokes this reader's access —
		// the same defect this refcount exists to close, reached across the
		// read/write boundary rather than within one side of it.
		for existing := range s.tempWrites {
			if pathWithinRoot(existing, root) {
				s.tempWrites[existing]++
				covering := existing
				return root, func() { s.releaseTemporaryWrite(covering) }, nil
			}
		}
		// Genuinely permanent — the workspace root, or a session-scoped grant.
		// Nothing to release.
		return root, func() {}, nil
	}
	for _, existing := range s.readRoots {
		if !pathWithinRoot(existing, root) {
			continue
		}
		// Already covered — but by WHAT decides whether this caller has
		// something to release. A permanent read root outlives every grant, so
		// the undo is genuinely nothing. A TEMPORARY one is held by another
		// caller who will release it, and that caller must not be able to
		// revoke this one's access: take a reference on the covering root, so
		// it survives until the last holder is done.
		if _, temporary := s.tempReads[existing]; temporary {
			s.tempReads[existing]++
			covering := existing
			return root, func() { s.releaseTemporaryRead(covering) }, nil
		}
		return root, func() {}, nil
	}
	s.readRoots = append(s.readRoots, root)
	if s.tempReads == nil {
		s.tempReads = map[string]int{}
	}
	s.tempReads[root] = 1
	return root, func() { s.releaseTemporaryRead(root) }, nil
}

func (s *Scope) AddTemporaryWrite(path string) (string, func(), error) {
	root, err := normalizeScopeRoot(path)
	if err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeRootCoversLocked(root) {
		// Covered already. If a TEMPORARY write grant is what covers it, take a
		// reference so the covering holder's cleanup cannot revoke this one —
		// the same rule as reads, and it has to be the same rule or a write
		// grant would keep the bug reads no longer have.
		for existing := range s.tempWrites {
			if pathWithinRoot(existing, root) {
				s.tempWrites[existing]++
				covering := existing
				return root, func() { s.releaseTemporaryWrite(covering) }, nil
			}
		}
		return root, func() {}, nil
	}
	s.extraRoots = append(s.extraRoots, root)
	if s.tempWrites == nil {
		s.tempWrites = map[string]int{}
	}
	s.tempWrites[root] = 1
	return root, func() { s.releaseTemporaryWrite(root) }, nil
}

func (s *Scope) writeRootCoversLocked(root string) bool {
	for _, existing := range append([]string{s.workspaceRoot}, s.extraRoots...) {
		if pathWithinRoot(existing, root) {
			return true
		}
	}
	return false
}

// releaseTemporaryRead drops one holder's reference and removes the root only
// when the last one is gone. IDEMPOTENT per holder is not the property here —
// each undo is called exactly once — but a double call must not remove a root
// another holder still needs, so the count floors at zero rather than going
// negative.
func (s *Scope) releaseTemporaryRead(root string) {
	s.mu.Lock()
	remaining, tracked := s.tempReads[root]
	if !tracked {
		s.mu.Unlock()
		return
	}
	remaining--
	if remaining > 0 {
		s.tempReads[root] = remaining
		s.mu.Unlock()
		return
	}
	// Both mutations under ONE hold. Dropping the lock between them opened a
	// window where the root was still in readRoots but no longer in tempReads:
	// a concurrent AddTemporaryRead landing there reads it as a PERMANENT root,
	// hands its caller a no-op undo, and then this call strips the root — so
	// that caller believes it holds access it has already silently lost.
	delete(s.tempReads, root)
	s.readRoots = removeScopeRoot(s.readRoots, root)
	s.mu.Unlock()
}

// releaseTemporaryWrite is releaseTemporaryRead for write roots. Two functions
// rather than one generic helper because they guard different slices and the
// generic version would take the slice by name — which is how the wrong one
// gets passed.
func (s *Scope) releaseTemporaryWrite(root string) {
	s.mu.Lock()
	remaining, tracked := s.tempWrites[root]
	if !tracked {
		s.mu.Unlock()
		return
	}
	remaining--
	if remaining > 0 {
		s.tempWrites[root] = remaining
		s.mu.Unlock()
		return
	}
	// Same single-hold rule as releaseTemporaryRead above.
	delete(s.tempWrites, root)
	s.extraRoots = removeScopeRoot(s.extraRoots, root)
	s.mu.Unlock()
}

func removeScopeRoot(roots []string, root string) []string {
	next := roots[:0]
	for _, existing := range roots {
		if existing != root {
			next = append(next, existing)
		}
	}
	return next
}

func dedupeScopeRoots(roots []string) []string {
	seen := map[string]struct{}{}
	out := roots[:0]
	for _, root := range roots {
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	return out
}

// validate reports whether requestedPath is allowed by any scope root.
// Relative paths resolve against the workspace root only; absolute paths are
// accepted if they validate (including per-segment symlink checks) under ANY
// root. A symlink whose final target lies inside any granted root is allowed —
// this is a deliberate semantic widening compared with single-root validation,
// because the true write target is inside an allowed root.
//
// When all roots deny, a BlockSymlinkTraversal result from any root is
// preferred over BlockOutsideWorkspace; the --add-dir hint is appended
// only on outside_workspace results. The returned block always carries
// the caller's original requestedPath.
func (s *Scope) validate(requestedPath string) *pathBlock {
	return withOutsideWorkspaceAccessReason(s.validateAgainstRoots(requestedPath, s.Roots()), requestedPath, SideEffectWrite)
}

func (s *Scope) validateRead(requestedPath string) *pathBlock {
	return withOutsideWorkspaceAccessReason(s.validateAgainstRoots(requestedPath, s.ReadRoots()), requestedPath, SideEffectRead)
}

func withOutsideWorkspaceAccessReason(block *pathBlock, requestedPath string, sideEffect SideEffect) *pathBlock {
	if block == nil || block.Code != BlockOutsideWorkspace || !strings.Contains(block.Reason, " is outside the workspace") {
		return block
	}
	switch sideEffect {
	case SideEffectRead:
		block.Reason = fmt.Sprintf("Reading %s requires access outside the workspace.", requestedPath)
	case SideEffectWrite, SideEffectOutOfWorkspace:
		block.Reason = fmt.Sprintf("Writing to %s requires access outside the workspace. Use /add-dir or --add-dir to allow writes there.", requestedPath)
	}
	return block
}

func (s *Scope) validateAgainstRoots(requestedPath string, roots []string) *pathBlock {
	if len(roots) == 0 {
		return &pathBlock{
			Code:   BlockOutsideWorkspace,
			Path:   requestedPath,
			Reason: fmt.Sprintf("%s is outside the workspace", requestedPath),
		}
	}
	if !filepath.IsAbs(requestedPath) {
		return validateWorkspacePath(roots[0], requestedPath)
	}
	// For each root, normalize the leading path prefix so that platform-level
	// symlinks (e.g. macOS /var -> /private/var) are resolved before comparing
	// against the symlink-resolved scope roots, while leaving workspace-internal
	// symlinks intact so validateWorkspacePath can detect traversal blocks.
	var outsideBlock *pathBlock
	var traversalBlock *pathBlock
	for _, root := range roots {
		normalized := NormalizePrefixForRoot(requestedPath, root)
		block := validateWorkspacePath(root, normalized)
		if block == nil {
			return nil
		}
		switch block.Code {
		case BlockSymlinkTraversal:
			if traversalBlock == nil {
				traversalBlock = block
			}
		default:
			if outsideBlock == nil {
				outsideBlock = block
			}
		}
	}
	// Prefer symlink-traversal: the path was lexically inside a granted root
	// but crossed an in-root symlink — the --add-dir hint would be misleading.
	if traversalBlock != nil {
		return &pathBlock{
			Code:   BlockSymlinkTraversal,
			Path:   requestedPath,
			Reason: traversalBlock.Reason,
		}
	}
	// Plain outside-workspace denial — rebuild with the original path and hint.
	return &pathBlock{
		Code:   BlockOutsideWorkspace,
		Path:   requestedPath,
		Reason: fmt.Sprintf("%s is outside the workspace (use /add-dir or --add-dir to allow writes there)", requestedPath),
	}
}

// NormalizePrefixForRoot resolves platform-level symlinks (e.g. macOS
// /var -> /private/var) in the portion of absPath that lies outside
// resolvedRoot, while leaving workspace-internal path components intact so
// that validateWorkspacePath can detect symlink traversal blocks there.
// It is exported because the tools layer shares it to normalize absolute
// paths per scope root before running its own single-root checks.
//
// Algorithm: walk absPath component-by-component, resolving each via
// EvalSymlinks. Once the running resolved prefix equals resolvedRoot we are
// inside the root — stop resolving and append the remaining components
// verbatim. If a component inside the root is a symlink, leave it for
// validateWorkspacePath to handle. Non-existent tail components are always
// appended verbatim.
//
// The walk is volume-aware so it works on Windows as well as POSIX. On
// Windows the same alias problem appears in a different guise — a workspace
// created under an 8.3 short path (C:\Users\RUNNER~1\...) is resolved by
// EvalSymlinks to its long form (C:\Users\runneradmin\...), so a raw
// short-form request would escape the long-form root unless its prefix is
// resolved here first. The component walk must therefore start from the
// volume root (C:\ or \\host\share\), not "/", or it would mangle a drive
// path into a drive-relative form (C:\Users -> C:Users) that the single-root
// checks treat as RELATIVE — failing the policy gate OPEN. On POSIX
// VolumeName is empty and the volume root reduces to "/", so behavior there
// is byte-identical to a plain "/"-rooted walk.
func NormalizePrefixForRoot(absPath, resolvedRoot string) string {
	volume := filepath.VolumeName(absPath)
	volumeRoot := volume + string(filepath.Separator)
	tail := strings.TrimPrefix(filepath.Clean(absPath), volume)
	parts := strings.Split(strings.TrimPrefix(tail, string(filepath.Separator)), string(filepath.Separator))
	current := volumeRoot
	for i, part := range parts {
		if part == "" {
			continue
		}
		// If we've reached the resolved root boundary, stop resolving and
		// append the remaining tail verbatim so validateWorkspacePath sees the
		// original symlink names.
		if current == resolvedRoot {
			return filepath.Join(append([]string{current}, parts[i:]...)...)
		}
		next := filepath.Join(current, part)
		info, lerr := os.Lstat(next)
		if lerr != nil {
			// Non-existent component — append rest verbatim.
			return filepath.Join(append([]string{current}, parts[i:]...)...)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Symlink. Only resolve it if we're still outside the root.
			if pathWithinRoot(resolvedRoot, current) {
				// Inside root — leave this symlink for validateWorkspacePath.
				return filepath.Join(append([]string{current}, parts[i:]...)...)
			}
			// Outside root (or a jump into the root) — resolve this platform-level symlink.
			resolved, err := filepath.EvalSymlinks(next)
			if err != nil {
				return filepath.Join(append([]string{current}, parts[i:]...)...)
			}
			current = resolved
			continue
		}
		// Regular component outside root — resolve it.
		resolved, err := filepath.EvalSymlinks(next)
		if err != nil {
			current = next
		} else {
			current = resolved
		}
	}
	return current
}

func normalizeWorkspaceRootBestEffort(workspaceRoot string) string {
	trimmed := strings.TrimSpace(workspaceRoot)
	if trimmed == "" {
		return ""
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return filepath.Clean(absolute)
}

func normalizeScopeRoot(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("write root path is empty")
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(trimmed[1:], "/"), string(filepath.Separator)))
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("write root must exist: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("write root %s is not a directory", resolved)
	}
	if filepath.Dir(resolved) == resolved {
		return "", fmt.Errorf("refusing filesystem root %s as a write root", resolved)
	}
	return resolved, nil
}

func defaultTempWriteRootCandidates() []string {
	return defaultTempWriteRootCandidatesForGOOS(runtime.GOOS, os.Getenv)
}

func defaultTempWriteRootCandidatesForGOOS(goos string, getenv func(string) string) []string {
	var roots []string
	if goos == "windows" {
		for _, key := range []string{"TEMP", "TMP"} {
			if root := strings.TrimSpace(getenv(key)); root != "" {
				roots = append(roots, root)
			}
		}
		return roots
	}
	if goos != "windows" {
		roots = append(roots, "/tmp")
	}
	if tmpdir := strings.TrimSpace(getenv("TMPDIR")); tmpdir != "" {
		roots = append(roots, tmpdir)
	}
	return roots
}

func defaultTempWriteRoots() []string {
	return normalizeProfileDirs(defaultTempWriteRootCandidates())
}

func pathWithinRoot(root string, candidate string) bool {
	if root == "" {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
