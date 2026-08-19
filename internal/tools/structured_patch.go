package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gitlawb/zero/internal/pathjail"
)

const (
	structuredPatchBegin  = "*** Begin Patch"
	structuredPatchEnd    = "*** End Patch"
	structuredAddFile     = "*** Add File: "
	structuredDeleteFile  = "*** Delete File: "
	structuredUpdateFile  = "*** Update File: "
	structuredMoveTo      = "*** Move to: "
	structuredEndOfFile   = "*** End of File"
	structuredHunkContext = "@@ "
)

type structuredPatchKind uint8

const (
	structuredPatchAdd structuredPatchKind = iota
	structuredPatchDelete
	structuredPatchUpdate
)

type structuredPatchOperation struct {
	kind     structuredPatchKind
	path     string
	movePath string
	contents string
	chunks   []structuredPatchChunk
	line     int
}

type structuredPatchChunk struct {
	context    string
	hasContext bool
	old        []string
	new        []string
	endOfFile  bool
}

type structuredPatchTarget struct {
	requested string
	absolute  string
	relative  string
}

type structuredPatchChange struct {
	kind   structuredPatchKind
	from   structuredPatchTarget
	to     structuredPatchTarget
	before string
	after  string
	mode   os.FileMode
}

func isStructuredPatch(patch string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(patch, "\ufeff")), structuredPatchBegin)
}

func (tool applyPatchTool) runStructuredPatch(applyRoot, relativeRoot, patch string, options RunOptions) Result {
	operations, err := parseStructuredPatch(patch)
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	workspace, err := os.OpenRoot(applyRoot)
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	defer workspace.Close()
	changes, err := planStructuredPatch(workspace, operations, options.FileTracker)
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	wholeBefore := make(map[string]bool, len(changes))
	if options.FileTracker != nil {
		for _, change := range changes {
			if change.kind == structuredPatchUpdate {
				wholeBefore[change.from.absolute] = options.FileTracker.SeenWhole(change.from.absolute)
			}
		}
	}
	if err := applyStructuredPatchChanges(workspace, changes, options.FileTracker); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	for _, change := range changes {
		switch change.kind {
		case structuredPatchDelete:
			options.FileTracker.Forget(change.from.absolute)
		case structuredPatchAdd:
			recordStructuredPatchFile(options.FileTracker, change.to.absolute, true, true)
		case structuredPatchUpdate:
			wasWhole := wholeBefore[change.from.absolute]
			options.FileTracker.Forget(change.from.absolute)
			recordStructuredPatchFile(options.FileTracker, change.to.absolute, false, wasWhole)
		}
	}

	summary := "Patch applied successfully."
	if relativeRoot != "." {
		summary = "Patch applied successfully in " + relativeRoot + "."
	}
	result := okResult(summary)
	result.ChangedFiles = changedFilesFromStructuredPatch(relativeRoot, changes)
	result.Display = Display{Summary: summary, Kind: "diff", Preview: structuredPatchPreview(changes)}
	return result
}

func parseStructuredPatch(patch string) ([]structuredPatchOperation, error) {
	normalized := strings.TrimSpace(strings.TrimPrefix(strings.ReplaceAll(patch, "\r\n", "\n"), "\ufeff"))
	if normalized == "" {
		return nil, fmt.Errorf("structured patch is empty")
	}
	lines := strings.Split(normalized, "\n")
	if strings.TrimSpace(lines[0]) != structuredPatchBegin {
		return nil, fmt.Errorf("the first line of a structured patch must be %q", structuredPatchBegin)
	}
	if strings.TrimSpace(lines[len(lines)-1]) != structuredPatchEnd {
		return nil, fmt.Errorf("the last line of a structured patch must be %q", structuredPatchEnd)
	}

	var operations []structuredPatchOperation
	for index := 1; index < len(lines)-1; {
		header := strings.TrimSpace(lines[index])
		lineNumber := index + 1
		if header == "" {
			index++
			continue
		}
		switch {
		case strings.HasPrefix(header, structuredAddFile):
			path, err := structuredPatchPath(header, structuredAddFile, lineNumber)
			if err != nil {
				return nil, err
			}
			op := structuredPatchOperation{kind: structuredPatchAdd, path: path, line: lineNumber}
			index++
			var content []string
			for index < len(lines)-1 && !isStructuredPatchHeader(lines[index]) {
				if !strings.HasPrefix(lines[index], "+") {
					return nil, structuredPatchLineError(index+1, "added-file contents must start with '+'")
				}
				content = append(content, strings.TrimPrefix(lines[index], "+"))
				index++
			}
			if len(content) > 0 {
				op.contents = strings.Join(content, "\n") + "\n"
			}
			operations = append(operations, op)
		case strings.HasPrefix(header, structuredDeleteFile):
			path, err := structuredPatchPath(header, structuredDeleteFile, lineNumber)
			if err != nil {
				return nil, err
			}
			operations = append(operations, structuredPatchOperation{kind: structuredPatchDelete, path: path, line: lineNumber})
			index++
		case strings.HasPrefix(header, structuredUpdateFile):
			path, err := structuredPatchPath(header, structuredUpdateFile, lineNumber)
			if err != nil {
				return nil, err
			}
			op := structuredPatchOperation{kind: structuredPatchUpdate, path: path, line: lineNumber}
			index++
			if index < len(lines)-1 && strings.HasPrefix(strings.TrimSpace(lines[index]), structuredMoveTo) {
				op.movePath, err = structuredPatchPath(strings.TrimSpace(lines[index]), structuredMoveTo, index+1)
				if err != nil {
					return nil, err
				}
				index++
			}
			for index < len(lines)-1 && !isStructuredPatchHeader(lines[index]) {
				line := lines[index]
				trimmed := strings.TrimSpace(line)
				if trimmed == structuredEndOfFile {
					if len(op.chunks) == 0 || (!hasStructuredPatchContent(op.chunks[len(op.chunks)-1])) {
						return nil, structuredPatchLineError(index+1, "*** End of File must follow an update hunk")
					}
					op.chunks[len(op.chunks)-1].endOfFile = true
					index++
					continue
				}
				if trimmed == "@@" || strings.HasPrefix(trimmed, structuredHunkContext) {
					chunk := structuredPatchChunk{}
					if trimmed != "@@" {
						chunk.context = strings.TrimPrefix(trimmed, structuredHunkContext)
						chunk.hasContext = true
					}
					op.chunks = append(op.chunks, chunk)
					index++
					continue
				}
				if len(op.chunks) == 0 {
					op.chunks = append(op.chunks, structuredPatchChunk{})
				}
				chunk := &op.chunks[len(op.chunks)-1]
				switch {
				case line == "":
					chunk.old = append(chunk.old, "")
					chunk.new = append(chunk.new, "")
				case strings.HasPrefix(line, " "):
					content := strings.TrimPrefix(line, " ")
					chunk.old = append(chunk.old, content)
					chunk.new = append(chunk.new, content)
				case strings.HasPrefix(line, "+"):
					chunk.new = append(chunk.new, strings.TrimPrefix(line, "+"))
				case strings.HasPrefix(line, "-"):
					chunk.old = append(chunk.old, strings.TrimPrefix(line, "-"))
				default:
					return nil, structuredPatchLineError(index+1, "update lines must start with ' ', '+', or '-'")
				}
				index++
			}
			if len(op.chunks) == 0 || !allStructuredPatchChunksHaveContent(op.chunks) {
				return nil, structuredPatchLineError(lineNumber, "update file hunk is empty")
			}
			operations = append(operations, op)
		default:
			return nil, structuredPatchLineError(lineNumber, "expected an Add File, Delete File, or Update File header")
		}
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("structured patch contains no file operations")
	}
	return operations, nil
}

func structuredPatchPath(header, prefix string, line int) (string, error) {
	path := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if path == "" {
		return "", structuredPatchLineError(line, "file path is required")
	}
	return path, nil
}

func isStructuredPatchHeader(line string) bool {
	return line == structuredPatchEnd || strings.HasPrefix(line, structuredAddFile) || strings.HasPrefix(line, structuredDeleteFile) || strings.HasPrefix(line, structuredUpdateFile)
}

func structuredPatchLineError(line int, message string) error {
	return fmt.Errorf("invalid structured patch at line %d: %s", line, message)
}

func hasStructuredPatchContent(chunk structuredPatchChunk) bool {
	return len(chunk.old) > 0 || len(chunk.new) > 0
}

func allStructuredPatchChunksHaveContent(chunks []structuredPatchChunk) bool {
	for _, chunk := range chunks {
		if !hasStructuredPatchContent(chunk) {
			return false
		}
	}
	return true
}

func structuredPatchOperationPaths(operations []structuredPatchOperation) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, operation := range operations {
		for _, path := range []string{operation.path, operation.movePath} {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func planStructuredPatch(root *os.Root, operations []structuredPatchOperation, tracker *FileTracker) ([]structuredPatchChange, error) {
	seen := make(map[string]struct{})
	var changes []structuredPatchChange
	for _, operation := range operations {
		from, err := resolveStructuredPatchTarget(root.Name(), operation.path)
		if err != nil {
			return nil, err
		}
		to := from
		if operation.movePath != "" {
			to, err = resolveStructuredPatchTarget(root.Name(), operation.movePath)
			if err != nil {
				return nil, err
			}
		}
		targets := []structuredPatchTarget{from}
		if to.absolute != from.absolute {
			targets = append(targets, to)
		}
		for _, target := range targets {
			if _, exists := seen[target.absolute]; exists {
				return nil, fmt.Errorf("structured patch changes %q more than once", target.relative)
			}
			seen[target.absolute] = struct{}{}
		}

		change := structuredPatchChange{kind: operation.kind, from: from, to: to, mode: 0o644}
		switch operation.kind {
		case structuredPatchAdd:
			if _, err := root.Lstat(to.relative); err == nil {
				return nil, fmt.Errorf("cannot add %s because it already exists", to.relative)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
			change.after = operation.contents
		case structuredPatchDelete, structuredPatchUpdate:
			info, err := root.Stat(from.relative)
			if err != nil {
				return nil, fmt.Errorf("stating %s: %w", from.relative, err)
			}
			change.mode = info.Mode()
			content, err := root.ReadFile(from.relative)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", from.relative, err)
			}
			if err := tracker.CheckConflict(from.absolute, content); err != nil {
				return nil, fmt.Errorf("%s", fileConflictMessage(from.relative))
			}
			change.before = string(content)
			if operation.kind == structuredPatchUpdate {
				updated, err := applyStructuredPatchUpdate(change.before, from.relative, operation.chunks)
				if err != nil {
					return nil, err
				}
				change.after = updated
				if from.absolute != to.absolute {
					if _, err := root.Lstat(to.relative); err == nil {
						return nil, fmt.Errorf("cannot move %s to %s because the destination already exists", from.relative, to.relative)
					} else if !os.IsNotExist(err) {
						return nil, err
					}
				}
			}
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func resolveStructuredPatchTarget(root, path string) (structuredPatchTarget, error) {
	if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(filepath.ToSlash(path), "../") {
		return structuredPatchTarget{}, fmt.Errorf("patch path %q must stay inside the workspace", path)
	}
	absolute, relative, err := resolveWorkspaceTargetPath(root, path)
	if err != nil {
		return structuredPatchTarget{}, err
	}
	return structuredPatchTarget{requested: path, absolute: absolute, relative: relative}, nil
}

func applyStructuredPatchUpdate(content, path string, chunks []structuredPatchChunk) (string, error) {
	lineEnding := structuredPatchLineEnding(content)
	if lineEnding == "\r\n" {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}
	lines := strings.Split(content, "\n")
	trailingNewline := content != "" && len(lines) > 0 && lines[len(lines)-1] == ""
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	replacements := make([]structuredReplacement, 0, len(chunks))
	lineIndex := 0
	for _, chunk := range chunks {
		if chunk.hasContext {
			index, ambiguous := findStructuredPatchSequence(lines, []string{chunk.context}, lineIndex, false)
			if ambiguous {
				return "", fmt.Errorf("context %q is ambiguous in %s; provide a more specific hunk header", chunk.context, path)
			}
			if index < 0 {
				return "", fmt.Errorf("could not find context %q in %s", chunk.context, path)
			}
			lineIndex = index + 1
		}
		if len(chunk.old) == 0 {
			// A context anchor identifies the line immediately before a pure
			// insertion. Only an explicit end-of-file hunk (or an unanchored
			// insertion) belongs at the file end.
			start := lineIndex
			if chunk.endOfFile || !chunk.hasContext {
				start = len(lines)
			}
			if start > len(lines) {
				start = len(lines)
			}
			replacements = append(replacements, structuredReplacement{start: start, replacement: append([]string(nil), chunk.new...)})
			lineIndex = start
			continue
		}
		index, ambiguous := findStructuredPatchSequence(lines, chunk.old, lineIndex, chunk.endOfFile)
		if ambiguous {
			return "", fmt.Errorf("expected lines are ambiguous in %s; provide more surrounding context:\n%s", path, strings.Join(chunk.old, "\n"))
		}
		if index < 0 {
			return "", fmt.Errorf("could not find expected lines in %s:\n%s", path, strings.Join(chunk.old, "\n"))
		}
		replacements = append(replacements, structuredReplacement{start: index, length: len(chunk.old), replacement: append([]string(nil), chunk.new...)})
		lineIndex = index + len(chunk.old)
	}
	for index := 1; index < len(replacements); index++ {
		previous := replacements[index-1]
		current := replacements[index]
		if current.start < previous.start+previous.length {
			return "", fmt.Errorf("overlapping or out-of-order hunks in %s; provide non-overlapping context", path)
		}
	}
	for index := len(replacements) - 1; index >= 0; index-- {
		replacement := replacements[index]
		lines = append(lines[:replacement.start], append(replacement.replacement, lines[replacement.start+replacement.length:]...)...)
	}
	if len(lines) == 0 {
		return "", nil
	}
	updated := strings.Join(lines, lineEnding)
	if trailingNewline {
		updated += lineEnding
	}
	return updated, nil
}

func structuredPatchLineEnding(content string) string {
	if strings.Contains(content, "\r\n") && !strings.Contains(strings.ReplaceAll(content, "\r\n", ""), "\n") {
		return "\r\n"
	}
	return "\n"
}

type structuredReplacement struct {
	start       int
	length      int
	replacement []string
}

func findStructuredPatchSequence(lines, wanted []string, start int, endOfFile bool) (int, bool) {
	if len(wanted) == 0 {
		return start, false
	}
	if len(wanted) > len(lines) {
		return -1, false
	}
	searchStart := start
	if endOfFile {
		searchStart = len(lines) - len(wanted)
		if searchStart < start {
			searchStart = start
		}
	}
	for _, equal := range []func(string, string) bool{
		func(left, right string) bool { return left == right },
		func(left, right string) bool {
			return strings.TrimRight(left, " \t") == strings.TrimRight(right, " \t")
		},
		func(left, right string) bool { return strings.TrimSpace(left) == strings.TrimSpace(right) },
	} {
		match := -1
		for index := searchStart; index+len(wanted) <= len(lines); index++ {
			matched := true
			for offset, line := range wanted {
				if !equal(lines[index+offset], line) {
					matched = false
					break
				}
			}
			if matched {
				if match >= 0 {
					return -1, true
				}
				match = index
			}
		}
		if match >= 0 {
			return match, false
		}
	}
	return -1, false
}

func applyStructuredPatchChanges(root *os.Root, changes []structuredPatchChange, tracker *FileTracker) error {
	mutated := false
	for _, change := range changes {
		committed, err := applyStructuredPatchChange(root, change)
		mutated = mutated || committed
		if err != nil {
			forgetStructuredPatchFiles(tracker, changes)
			if mutated {
				return fmt.Errorf("%w; structured patch was partially applied; re-read affected files before retrying", err)
			}
			return err
		}
	}
	return nil
}

func forgetStructuredPatchFiles(tracker *FileTracker, changes []structuredPatchChange) {
	if tracker == nil {
		return
	}
	for _, change := range changes {
		tracker.Forget(change.from.absolute)
		if change.to.absolute != change.from.absolute {
			tracker.Forget(change.to.absolute)
		}
	}
}

func applyStructuredPatchChange(root *os.Root, change structuredPatchChange) (bool, error) {
	switch change.kind {
	case structuredPatchDelete:
		if err := root.Remove(change.from.relative); err != nil {
			return false, fmt.Errorf("deleting %s: %w", change.from.relative, err)
		}
		return true, nil
	case structuredPatchAdd:
		return writeStructuredPatchFile(root, change.to, change.after, change.mode, true)
	case structuredPatchUpdate:
		moving := change.from.absolute != change.to.absolute
		committed, err := writeStructuredPatchFile(root, change.to, change.after, change.mode, moving)
		if err != nil {
			return committed, err
		}
		if moving {
			if err := root.Remove(change.from.relative); err != nil {
				return true, fmt.Errorf("removing moved source %s: %w", change.from.relative, err)
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("unsupported structured patch operation")
}

func writeStructuredPatchFile(root *os.Root, target structuredPatchTarget, content string, mode os.FileMode, createOnly bool) (bool, error) {
	parent := filepath.Dir(target.relative)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return false, fmt.Errorf("creating parent directory for %s: %w", target.relative, err)
	}
	temp, tempName, err := pathjail.CreateTemp(root, parent, "zero-patch", ".tmp")
	if err != nil {
		return false, fmt.Errorf("writing %s: %w", target.relative, err)
	}
	defer func() { _ = removeStructuredPatchTemp(root, tempName) }()
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("writing %s: %w", target.relative, err)
	}
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("writing %s: %w", target.relative, err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("writing %s: %w", target.relative, err)
	}
	if createOnly {
		return publishStructuredPatchNoReplace(root, tempName, target.relative, mode)
	}
	if err := root.Rename(tempName, target.relative); err != nil {
		return false, fmt.Errorf("writing %s: %w", target.relative, err)
	}
	return true, nil
}

func publishStructuredPatchNoReplace(root *os.Root, source, target string, mode os.FileMode) (bool, error) {
	return publishStructuredPatchNoReplaceWith(root, source, target, mode, root.Link)
}

func publishStructuredPatchNoReplaceWith(root *os.Root, source, target string, mode os.FileMode, link func(string, string) error) (bool, error) {
	if linkErr := link(source, target); linkErr == nil {
		cleanupErr := removeStructuredPatchTemp(root, source)
		chmodErr := root.Chmod(target, mode.Perm())
		if cleanupErr != nil || chmodErr != nil {
			return true, fmt.Errorf("publishing %s: %w", target, errors.Join(cleanupErr, chmodErr))
		}
		return true, nil
	} else if errors.Is(linkErr, os.ErrExist) {
		return false, fmt.Errorf("creating %s: %w", target, linkErr)
	} else {
		committed, copyErr := copyStructuredPatchNoReplace(root, source, target, mode)
		if copyErr != nil {
			return committed, fmt.Errorf("creating %s without overwrite: hard link failed: %w; exclusive-copy fallback failed: %w", target, linkErr, copyErr)
		}
		return committed, nil
	}
}

func copyStructuredPatchNoReplace(root *os.Root, source, target string, mode os.FileMode) (bool, error) {
	return copyStructuredPatchNoReplaceWith(root, source, target, mode, io.Copy, removeStructuredPatchTemp)
}

func copyStructuredPatchNoReplaceWith(
	root *os.Root,
	source, target string,
	mode os.FileMode,
	copyFile func(io.Writer, io.Reader) (int64, error),
	cleanup func(*os.Root, string) error,
) (bool, error) {
	sourceFile, err := root.Open(source)
	if err != nil {
		return false, err
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed {
			_ = sourceFile.Close()
		}
	}()
	targetFile, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	committed := true
	cleanupTarget := func(cause error) (bool, error) {
		_ = targetFile.Close()
		if cleanupErr := cleanup(root, target); cleanupErr == nil {
			return false, cause
		} else {
			return committed, errors.Join(cause, fmt.Errorf("cleaning partial target: %w", cleanupErr))
		}
	}
	if _, err := copyFile(targetFile, sourceFile); err != nil {
		return cleanupTarget(err)
	}
	if err := sourceFile.Close(); err != nil {
		sourceClosed = true
		return cleanupTarget(err)
	}
	sourceClosed = true
	if err := targetFile.Chmod(mode.Perm()); err != nil {
		return cleanupTarget(err)
	}
	if err := targetFile.Close(); err != nil {
		if cleanupErr := cleanup(root, target); cleanupErr == nil {
			return false, err
		} else {
			return true, errors.Join(err, fmt.Errorf("cleaning target after close failure: %w", cleanupErr))
		}
	}
	if err := cleanup(root, source); err != nil {
		return true, fmt.Errorf("removing published source: %w", err)
	}
	return true, nil
}

func removeStructuredPatchTemp(root *os.Root, name string) error {
	chmodErr := root.Chmod(name, 0o600)
	if os.IsNotExist(chmodErr) {
		return nil
	}
	removeErr := root.Remove(name)
	if os.IsNotExist(removeErr) {
		removeErr = nil
	}
	if removeErr == nil {
		return nil
	}
	return errors.Join(chmodErr, removeErr)
}

func recordStructuredPatchFile(tracker *FileTracker, absolute string, created, seenWhole bool) {
	if tracker == nil {
		return
	}
	content, err := os.ReadFile(absolute)
	if err != nil {
		tracker.Forget(absolute)
		return
	}
	info, _ := os.Stat(absolute)
	tracker.Record(absolute, content, info)
	if seenWhole {
		lines := lineCount(string(content))
		tracker.RecordSeenRange(absolute, 1, lines, lines)
	}
	if created {
		tracker.RecordCreated(absolute)
	}
}

func changedFilesFromStructuredPatch(relativeRoot string, changes []structuredPatchChange) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, change := range changes {
		targets := []structuredPatchTarget{change.to}
		if change.from.absolute != change.to.absolute {
			targets = append([]structuredPatchTarget{change.from}, targets...)
		}
		for _, target := range targets {
			path := target.relative
			if relativeRoot != "" && relativeRoot != "." {
				path = filepath.ToSlash(filepath.Join(relativeRoot, path))
			}
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func structuredPatchPreview(changes []structuredPatchChange) string {
	var preview strings.Builder
	for _, change := range changes {
		path := change.to.relative
		if change.kind == structuredPatchDelete {
			path = change.from.relative
		}
		before, after := change.before, change.after
		if change.kind == structuredPatchDelete {
			after = ""
		}
		diff := boundedUnifiedDiff(path, before, after)
		if diff == "" {
			continue
		}
		if preview.Len()+len(diff)+1 > maxToolPreviewBytes {
			break
		}
		if preview.Len() > 0 {
			preview.WriteByte('\n')
		}
		preview.WriteString(diff)
	}
	return capPreviewDiff(preview.String())
}
