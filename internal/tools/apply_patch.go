package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/sandbox"
)

type applyPatchTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewScopedApplyPatchTool(workspaceRoot string, scope PathScope) Tool {
	return applyPatchTool{
		baseTool: baseTool{
			name:        "apply_patch",
			description: "Apply a patch inside the workspace or an explicitly granted extra write root.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"patch": {Type: "string", Description: "A unified diff or a structured *** Begin Patch patch to apply."},
					"cwd":   {Type: "string", Description: "Directory where the patch should be applied. Relative paths stay in the workspace; use an absolute path to target a granted extra write root. Defaults to workspace root.", Default: "."},
				},
				Required:             []string{"patch"},
				AdditionalProperties: false,
			},
			safety:       promptSafety(SideEffectWrite, "Applies patch hunks that can create, edit, or delete files."),
			capabilities: ToolCapabilities{Effect: EffectWorkspaceWrite, ThreadSafe: false, ResourceKeys: applyPatchResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool applyPatchTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool applyPatchTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	patch, err := aliasedStringArg(args, []string{"patch", "diff"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for apply_patch: " + err.Error())
	}
	cwd, err := stringArg(args, "cwd", ".", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for apply_patch: " + err.Error())
	}

	applyRoot, relativeRoot, err := resolveScopedPath(tool.workspaceRoot, tool.scope, cwd)
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	if isStructuredPatch(patch) {
		return tool.runStructuredPatch(applyRoot, relativeRoot, patch, options)
	}
	patchPaths, err := sandbox.PatchHeaderPaths(patch)
	if err != nil {
		return errorResult("Error applying patch: patch paths cannot be established safely: " + err.Error())
	}
	if err := validatePatchPaths(applyRoot, patchPaths); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	tempFile, err := os.CreateTemp("", "zero-patch-*.patch")
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	patchPath := tempFile.Name()
	defer func() {
		_ = os.Remove(patchPath)
	}()
	if _, err := tempFile.WriteString(patch); err != nil {
		_ = tempFile.Close()
		return errorResult("Error applying patch: " + err.Error())
	}
	if err := tempFile.Close(); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	if err := recheckPatchWriteTargets(applyRoot, patchPaths); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	var createdTargets []string
	var fullySuppliedTargets []string
	wholeBefore := map[string]bool{}
	if options.FileTracker != nil {
		createdTargets = missingPatchTargets(applyRoot, patchPaths)
		fullySuppliedTargets = completeCreatedPatchTargets(applyRoot, patch)
		for _, path := range patchPaths {
			if path == "" || path == "/dev/null" {
				continue
			}
			if absolute, _, rerr := resolveWorkspaceTargetPath(applyRoot, path); rerr == nil {
				wholeBefore[absolute] = options.FileTracker.SeenWhole(absolute)
			}
		}
	}

	if err := applyUnifiedPatchStaged(ctx, applyRoot, patchPath, patchPaths); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	summary := "Patch applied successfully."
	if relativeRoot != "." {
		summary = "Patch applied successfully in " + relativeRoot + "."
	}
	result := okResult(summary)
	result.ChangedFiles = changedFilesFromPatch(relativeRoot, patchPaths)
	result.Display = Display{Summary: summary, Kind: "diff", Preview: capPreviewDiff(patch)}
	fullySupplied := make(map[string]bool, len(fullySuppliedTargets))
	for _, absolute := range fullySuppliedTargets {
		fullySupplied[absolute] = true
	}
	// Re-baseline files changed by this tool. When the model had already seen the
	// whole input (or supplied a complete new file), the exact patch plus that
	// input determines the whole output, so a follow-up edit needs no wasted read.
	// Partial observations remain conservative and are cleared by Record.
	for _, changed := range result.ChangedFiles {
		target, rerr := resolveScopedReadTarget(tool.workspaceRoot, tool.scope, changed)
		if rerr != nil {
			continue
		}
		root, openErr := os.OpenRoot(target.root)
		if openErr != nil {
			options.FileTracker.Forget(target.absolute)
			continue
		}
		file, info, readErr := protectedRootRead(root, target.relative, target.absolute, tool.workspaceRoot)
		_ = root.Close()
		if readErr != nil {
			options.FileTracker.Forget(target.absolute)
			continue
		}
		content, contentErr := io.ReadAll(file)
		closeErr := file.Close()
		if contentErr != nil || closeErr != nil {
			options.FileTracker.Forget(target.absolute)
			continue
		}
		wasWhole := wholeBefore[target.absolute] || fullySupplied[target.absolute]
		options.FileTracker.Record(target.absolute, content, info)
		if wasWhole {
			lines := lineCount(string(content))
			options.FileTracker.RecordSeenRange(target.absolute, 1, lines, lines)
		}
	}
	recordCreatedPatchTargets(options.FileTracker, createdTargets)
	return result
}

type stagedPatchFile struct {
	exists  bool
	mode    os.FileMode
	content []byte
	link    string
}

func applyUnifiedPatchStaged(ctx context.Context, rootPath, patchPath string, patchPaths []string) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	stageDir, err := os.MkdirTemp("", "zero-patch-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)
	stage, err := os.OpenRoot(stageDir)
	if err != nil {
		return err
	}
	defer stage.Close()

	targets := make([]structuredPatchTarget, 0, len(patchPaths))
	before := make(map[string]stagedPatchFile, len(patchPaths))
	seen := make(map[string]bool, len(patchPaths))
	for _, path := range patchPaths {
		if path == "" || path == "/dev/null" || seen[path] {
			continue
		}
		seen[path] = true
		target, err := resolveStructuredPatchTarget(rootPath, path)
		if err != nil {
			return err
		}
		snapshot, err := captureRootedPatchFile(root, target, rootPath)
		if err != nil {
			return err
		}
		if snapshot.exists {
			if err := materializeStagedPatchFile(stage, target.relative, snapshot, true); err != nil {
				return err
			}
		}
		targets = append(targets, target)
		before[target.relative] = snapshot
	}

	command := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", patchPath)
	command.Dir = stageDir
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}

	after := make(map[string]stagedPatchFile, len(targets))
	for _, target := range targets {
		snapshot, err := captureRootedPatchFile(stage, target, stageDir)
		if err != nil {
			return err
		}
		after[target.relative] = snapshot
	}
	// Publish complete outputs first, then removals. A rename/copy destination
	// that raced into existence fails before its source is removed.
	for _, target := range targets {
		snapshot := after[target.relative]
		if !snapshot.exists {
			continue
		}
		if err := recheckWorkspaceWriteTarget(rootPath, target.relative); err != nil {
			return err
		}
		if err := protectedMutationDenied(target.absolute, rootPath); err != nil {
			return err
		}
		if err := materializeStagedPatchFile(root, target.relative, snapshot, !before[target.relative].exists); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if !before[target.relative].exists || after[target.relative].exists {
			continue
		}
		if err := recheckWorkspaceWriteTarget(rootPath, target.relative); err != nil {
			return err
		}
		if err := protectedMutationDenied(target.absolute, rootPath); err != nil {
			return err
		}
		if err := root.Remove(target.relative); err != nil {
			return err
		}
	}
	return nil
}

func captureRootedPatchFile(root *os.Root, target structuredPatchTarget, workspaceRoot string) (stagedPatchFile, error) {
	info, err := root.Lstat(target.relative)
	if os.IsNotExist(err) {
		return stagedPatchFile{}, nil
	}
	if err != nil {
		return stagedPatchFile{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := root.Readlink(target.relative)
		if err != nil {
			return stagedPatchFile{}, err
		}
		return stagedPatchFile{exists: true, mode: info.Mode(), link: link}, nil
	}
	if !info.Mode().IsRegular() {
		return stagedPatchFile{}, fmt.Errorf("patch target %q is not a regular file or symlink", target.relative)
	}
	file, openedInfo, err := protectedRootRead(root, target.relative, target.absolute, workspaceRoot)
	if err != nil {
		return stagedPatchFile{}, err
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return stagedPatchFile{}, errors.Join(readErr, closeErr)
	}
	return stagedPatchFile{exists: true, mode: openedInfo.Mode(), content: content}, nil
}

func materializeStagedPatchFile(root *os.Root, relative string, file stagedPatchFile, createOnly bool) error {
	if file.mode&os.ModeSymlink != 0 {
		return publishRootedSymlink(root, relative, file.link, createOnly)
	}
	_, err := writeRootedFile(root, relative, file.content, file.mode, createOnly)
	return err
}

func publishRootedSymlink(root *os.Root, relative, target string, createOnly bool) error {
	parent := filepath.Dir(relative)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if createOnly {
		return root.Symlink(target, relative)
	}
	for range 10 {
		noise := make([]byte, 8)
		if _, err := rand.Read(noise); err != nil {
			return err
		}
		temp := filepath.Join(parent, ".zero-link."+hex.EncodeToString(noise))
		if err := root.Symlink(target, temp); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return err
		}
		if err := root.Rename(temp, relative); err != nil {
			_ = root.Remove(temp)
			return err
		}
		return nil
	}
	return errors.New("could not create a temporary symlink with an unused name")
}

func missingPatchTargets(root string, patchPaths []string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, path := range patchPaths {
		if path == "" || path == "/dev/null" {
			continue
		}
		absolute, _, err := resolveWorkspaceTargetPath(root, path)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		if _, err := os.Stat(absolute); os.IsNotExist(err) {
			missing = append(missing, absolute)
		}
	}
	return missing
}

// completeCreatedPatchTargets returns only files whose full contents are
// supplied by a /dev/null creation patch. A missing rename/copy destination is
// created by git too, but its bytes come from an unread source and must not gain
// whole-file observation credit.
func completeCreatedPatchTargets(root string, patch string) []string {
	seen := map[string]bool{}
	var created []string
	oldRemaining, newRemaining := 0, 0
	inHunk := false
	fromDevNull := false
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if inHunk && (oldRemaining > 0 || newRemaining > 0) {
			switch {
			case strings.HasPrefix(line, "-"):
				oldRemaining--
			case strings.HasPrefix(line, "+"):
				newRemaining--
			case strings.HasPrefix(line, "\\"):
			default:
				oldRemaining--
				newRemaining--
			}
			continue
		}
		inHunk = false
		switch {
		case strings.HasPrefix(line, "diff --git "):
			fromDevNull = false
		case strings.HasPrefix(line, "@@"):
			oldRemaining, newRemaining = parseHunkCounts(line)
			inHunk = oldRemaining > 0 || newRemaining > 0
		case strings.HasPrefix(line, "--- "):
			fromDevNull = patchFileHeaderPath(line) == "/dev/null"
		case strings.HasPrefix(line, "+++ "):
			path := patchFileHeaderPath(line)
			if !fromDevNull || path == "" || path == "/dev/null" {
				fromDevNull = false
				continue
			}
			fromDevNull = false
			absolute, _, err := resolveWorkspaceTargetPath(root, stripPatchPrefix(path))
			if err != nil || seen[absolute] {
				continue
			}
			if _, err := os.Stat(absolute); !os.IsNotExist(err) {
				continue
			}
			seen[absolute] = true
			created = append(created, absolute)
		}
	}
	return created
}

func recordCreatedPatchTargets(tracker *FileTracker, missingBefore []string) {
	if tracker == nil {
		return
	}
	for _, absolute := range missingBefore {
		if _, err := os.Stat(absolute); err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		}
		tracker.RecordCreated(absolute)
	}
}

// changedFilesFromPatch extracts the unique, WORKSPACE-relative paths a patch
// touches, reusing the same per-line parser used for validation. Patch paths are
// relative to the apply cwd, so relativeRoot (the workspace-relative cwd, e.g.
// "sub/dir", or "." for the workspace root) is prefixed so callers get true
// workspace-relative paths regardless of cwd. When the apply cwd resolves to an
// extra write root, resolveScopedPath returns the absolute path as relativeRoot;
// in that case the entries in the returned slice are absolute paths, since
// workspace-relative would be ambiguous there.
func changedFilesFromPatch(relativeRoot string, patchPaths []string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, path := range patchPaths {
		if path == "" || path == "/dev/null" {
			continue
		}
		workspacePath := path
		if relativeRoot != "" && relativeRoot != "." {
			workspacePath = filepath.ToSlash(filepath.Join(relativeRoot, path))
		}
		if seen[workspacePath] {
			continue
		}
		seen[workspacePath] = true
		paths = append(paths, workspacePath)
	}
	return paths
}

func validatePatchPaths(root string, patchPaths []string) error {
	for _, path := range patchPaths {
		if path == "" || path == "/dev/null" {
			continue
		}
		if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
			return fmt.Errorf("patch path %q must stay inside the workspace", path)
		}
		absolute, _, err := resolveWorkspaceTargetPath(root, path)
		if err != nil {
			return err
		}
		// Engine-independent: the git-apply flow below shells out to an external
		// process this package cannot bind a handle to, so unlike write_file/
		// edit_file this is pathname-level protection only — see
		// internal/tools/protected_credentials.go. It is also the ONLY protection
		// this tool has when called through the plain registry API.
		if err := protectedMutationDenied(absolute, root); err != nil {
			return err
		}
	}
	return nil
}

func recheckPatchWriteTargets(root string, patchPaths []string) error {
	for _, path := range patchPaths {
		if path == "" || path == "/dev/null" {
			continue
		}
		if err := recheckWorkspaceWriteTarget(root, path); err != nil {
			return err
		}
		// Re-checked immediately before git apply runs, narrowing (not closing —
		// git apply is an external process) the window between the initial
		// validatePatchPaths check and the actual write.
		if absolute, _, err := resolveWorkspaceTargetPath(root, path); err == nil {
			if err := protectedMutationDenied(absolute, root); err != nil {
				return err
			}
		}
	}
	return nil
}

func patchFileHeaderPath(line string) string {
	if len(line) < len("--- ") {
		return ""
	}
	rest := line[len("--- "):] // "--- " and "+++ " are both 4 bytes
	if tab := strings.IndexByte(rest, '\t'); tab >= 0 {
		rest = rest[:tab]
	}
	return unquoteGitPath(rest)
}

// parseHunkCounts reads the old/new line counts from a "@@ -a,b +c,d @@" header.
// A missing count (e.g. "@@ -a +c @@") means 1 per unified-diff convention.
//
// Only the range section BETWEEN the opening and closing "@@" is parsed. A hunk
// header may carry a free-form section heading after the closing "@@" (e.g.
// "@@ -1,1 +1,1 @@ func foo()"), and that text can itself contain "+"/"-"
// tokens. Scanning the whole line would let a crafted heading like
// "@@ -1,1 +1,1 @@ +1,999999" overwrite the real count, keep the parser stuck in
// hunk mode, and swallow later "--- "/"+++ " file headers so they escape
// validatePatchPaths / recheckPatchWriteTargets — a workspace-confinement bypass.
func parseHunkCounts(line string) (int, int) {
	_, rest, ok := strings.Cut(line, "@@")
	if !ok {
		return 0, 0
	}
	rangeSection := rest
	if before, _, ok := strings.Cut(rest, "@@"); ok {
		rangeSection = before // drop the section heading after the closing "@@"
	}
	old, next := 0, 0
	for _, field := range strings.Fields(rangeSection) {
		switch {
		case strings.HasPrefix(field, "-"):
			old = hunkCount(field[1:])
		case strings.HasPrefix(field, "+"):
			next = hunkCount(field[1:])
		}
	}
	return old, next
}

func hunkCount(spec string) int {
	if _, count, ok := strings.Cut(spec, ","); ok {
		if n, err := strconv.Atoi(count); err == nil {
			return n
		}
		return 0
	}
	return 1
}

// unquoteGitPath undoes git's C-style quoting of a diff path. Git wraps a path in
// double quotes and backslash-escapes special bytes (spaces, tabs, high bytes as
// octal) when it contains anything unusual; an unquoted path is returned as-is.
func unquoteGitPath(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted
		}
	}
	return s
}

func stripPatchPrefix(path string) string {
	// A unified-diff path carries exactly one of the a/ or b/ prefixes; strip a
	// single one so a real directory literally named "a" or "b" is preserved.
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return filepath.ToSlash(path)
}
