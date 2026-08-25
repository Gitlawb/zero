package tools

import (
	"fmt"
	"strings"
)

// parseUnifiedPatch converts a unified diff into the same operations a
// structured patch produces, so both formats are applied by Zero itself
// through an opened workspace root (descriptor-relative, no-follow writes)
// instead of handing pathnames to git apply after validation.
//
// Supported: file modifications, creations (--- /dev/null) and deletions
// (+++ /dev/null), multiple hunks per file, "\ No newline at end of file"
// markers, a/ and b/ prefixes, git C-quoted paths, CRLF patches, and git's
// "rename from/to" and "copy from/to" headers (with or without hunks). Headers
// git emits around a hunk (diff --git, index, mode lines) are skipped; binary
// patches are rejected.
func parseUnifiedPatch(patch string) ([]structuredPatchOperation, error) {
	normalized := strings.TrimPrefix(strings.ReplaceAll(patch, "\r\n", "\n"), "\ufeff")
	lines := strings.Split(normalized, "\n")

	var operations []structuredPatchOperation
	var current *structuredPatchOperation
	var chunk *structuredPatchChunk
	var added []string // collected "+" lines for a file creation
	oldPath, newPath := "", ""
	pendingFrom, pendingKind := "", structuredPatchUpdate
	oldRemaining, newRemaining := 0, 0
	inHunk := false
	lastSide := byte(0) // '+' or '-' for the most recent content line

	finish := func() error {
		if current == nil {
			return nil
		}
		switch current.kind {
		case structuredPatchAdd:
			if len(added) > 0 {
				current.contents = strings.Join(added, "\n")
				if current.eofNewline != eofNewlineAbsent {
					current.contents += "\n"
				}
			}
		case structuredPatchUpdate, structuredPatchCopy:
			if !allStructuredPatchChunksHaveContent(current.chunks) {
				return fmt.Errorf("unified diff for %s has an empty hunk", current.path)
			}
			if len(current.chunks) == 0 && current.movePath == "" {
				return fmt.Errorf("unified diff for %s has no hunk lines", current.path)
			}
		}
		operations = append(operations, *current)
		current, chunk, added = nil, nil, nil
		return nil
	}
	startFile := func(line int) error {
		if oldPath == "" || newPath == "" {
			return fmt.Errorf("invalid unified diff at line %d: hunk before a ---/+++ header pair", line)
		}
		// A ---/+++ pair after a rename/copy header names the same files; keep
		// accumulating that operation's hunks instead of starting a new one.
		if current != nil && current.movePath != "" && current.path == oldPath && (current.movePath == newPath || oldPath == newPath) {
			return nil
		}
		if err := finish(); err != nil {
			return err
		}
		op := structuredPatchOperation{line: line}
		switch {
		case oldPath == "/dev/null" && newPath == "/dev/null":
			return fmt.Errorf("invalid unified diff at line %d: both sides are /dev/null", line)
		case oldPath == "/dev/null":
			op.kind, op.path = structuredPatchAdd, newPath
		case newPath == "/dev/null":
			op.kind, op.path = structuredPatchDelete, oldPath
		default:
			op.kind, op.path = structuredPatchUpdate, oldPath
			if newPath != oldPath {
				op.movePath = newPath
			}
		}
		current = &op
		return nil
	}

	for index, raw := range lines {
		lineNumber := index + 1
		if inHunk && (oldRemaining > 0 || newRemaining > 0) {
			switch {
			case strings.HasPrefix(raw, "\\"):
				// "\ No newline at end of file" qualifies the previous line's side.
				if current != nil && lastSide == '+' {
					current.eofNewline = eofNewlineAbsent
				} else if current != nil && lastSide == '-' && current.eofNewline == eofNewlineKeep {
					current.eofNewline = eofNewlinePresent
				}
				continue
			case strings.HasPrefix(raw, "-"):
				oldRemaining--
				lastSide = '-'
				if chunk != nil {
					chunk.old = append(chunk.old, raw[1:])
				}
			case strings.HasPrefix(raw, "+"):
				newRemaining--
				lastSide = '+'
				if current != nil && current.kind == structuredPatchAdd {
					added = append(added, raw[1:])
				} else if chunk != nil {
					chunk.new = append(chunk.new, raw[1:])
					chunk.newSourceOffsets = append(chunk.newSourceOffsets, -1)
				}
			default:
				content := raw
				if strings.HasPrefix(raw, " ") {
					content = raw[1:]
				} else if raw != "" {
					return nil, fmt.Errorf("invalid unified diff at line %d: hunk lines must start with ' ', '+', '-' or '\\'", lineNumber)
				}
				oldRemaining--
				newRemaining--
				lastSide = ' '
				if chunk != nil {
					offset := len(chunk.old)
					chunk.old = append(chunk.old, content)
					chunk.new = append(chunk.new, content)
					chunk.newSourceOffsets = append(chunk.newSourceOffsets, offset)
				}
			}
			continue
		}
		inHunk = false
		trimmed := strings.TrimSpace(raw)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(raw, "\\"):
			// "\ No newline at end of file" directly after a hunk's last line.
			if current != nil && lastSide == '+' {
				current.eofNewline = eofNewlineAbsent
			} else if current != nil && lastSide == '-' && current.eofNewline == eofNewlineKeep {
				current.eofNewline = eofNewlinePresent
			}
			continue
		case strings.HasPrefix(raw, "diff --git "), strings.HasPrefix(raw, "index "),
			strings.HasPrefix(raw, "new file mode "), strings.HasPrefix(raw, "deleted file mode "),
			strings.HasPrefix(raw, "old mode "), strings.HasPrefix(raw, "new mode "):
			continue
		case strings.HasPrefix(raw, "similarity index "), strings.HasPrefix(raw, "dissimilarity index "):
			continue
		case strings.HasPrefix(raw, "rename from "), strings.HasPrefix(raw, "copy from "):
			pendingFrom = strings.TrimSpace(unquoteGitPath(strings.TrimPrefix(strings.TrimPrefix(raw, "rename from "), "copy from ")))
			pendingKind = structuredPatchUpdate
			if strings.HasPrefix(raw, "copy from ") {
				pendingKind = structuredPatchCopy
			}
			if pendingFrom == "" {
				return nil, fmt.Errorf("invalid unified diff at line %d: missing source path", lineNumber)
			}
		case strings.HasPrefix(raw, "rename to "), strings.HasPrefix(raw, "copy to "):
			to := strings.TrimSpace(unquoteGitPath(strings.TrimPrefix(strings.TrimPrefix(raw, "rename to "), "copy to ")))
			if pendingFrom == "" || to == "" {
				return nil, fmt.Errorf("invalid unified diff at line %d: rename/copy destination without a source", lineNumber)
			}
			if err := finish(); err != nil {
				return nil, err
			}
			current = &structuredPatchOperation{kind: pendingKind, path: pendingFrom, movePath: to, line: lineNumber}
			oldPath, newPath, pendingFrom = pendingFrom, to, ""
		case strings.HasPrefix(raw, "Binary files "), strings.HasPrefix(raw, "GIT binary patch"):
			return nil, fmt.Errorf("invalid unified diff at line %d: binary patches are not supported", lineNumber)
		case strings.HasPrefix(raw, "--- "):
			oldPath = stripPatchPrefix(patchFileHeaderPath(raw))
			newPath = ""
			if oldPath == "" {
				return nil, fmt.Errorf("invalid unified diff at line %d: missing path in --- header", lineNumber)
			}
		case strings.HasPrefix(raw, "+++ "):
			newPath = stripPatchPrefix(patchFileHeaderPath(raw))
			if newPath == "" {
				return nil, fmt.Errorf("invalid unified diff at line %d: missing path in +++ header", lineNumber)
			}
			if err := startFile(lineNumber); err != nil {
				return nil, err
			}
		case strings.HasPrefix(raw, "@@"):
			if current == nil {
				return nil, fmt.Errorf("invalid unified diff at line %d: hunk before a ---/+++ header pair", lineNumber)
			}
			oldStart, oldCount, newCount, ok := parseHunkRange(raw)
			if !ok {
				return nil, fmt.Errorf("invalid unified diff at line %d: malformed hunk header", lineNumber)
			}
			oldRemaining, newRemaining = oldCount, newCount
			inHunk = oldRemaining > 0 || newRemaining > 0
			lastSide = 0
			if current.kind == structuredPatchUpdate || current.kind == structuredPatchCopy {
				next := structuredPatchChunk{hasHint: true, hint: oldStart - 1}
				if oldCount == 0 {
					// A pure insertion's range names the line after which the
					// new lines go, so the insertion index is oldStart itself.
					next.hint = oldStart
				}
				if next.hint < 0 {
					next.hint = 0
				}
				current.chunks = append(current.chunks, next)
				chunk = &current.chunks[len(current.chunks)-1]
			}
		default:
			return nil, fmt.Errorf("invalid unified diff at line %d: unexpected %q", lineNumber, trimmed)
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("unified diff contains no file changes")
	}
	return operations, nil
}

// parseHunkRange reads "@@ -a[,b] +c[,d] @@" and returns a, b and d; a missing
// count means 1 per unified-diff convention.
func parseHunkRange(line string) (oldStart, oldCount, newCount int, ok bool) {
	_, rest, found := strings.Cut(line, "@@")
	if !found {
		return 0, 0, 0, false
	}
	rangeSection := rest
	if before, _, found := strings.Cut(rest, "@@"); found {
		rangeSection = before
	}
	fields := strings.Fields(rangeSection)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "-") || !strings.HasPrefix(fields[1], "+") {
		return 0, 0, 0, false
	}
	parse := func(spec string) (int, int, bool) {
		startText, countText, hasCount := strings.Cut(spec, ",")
		start, err := parseNonNegativeInt(startText)
		if err != nil {
			return 0, 0, false
		}
		count := 1
		if hasCount {
			if count, err = parseNonNegativeInt(countText); err != nil {
				return 0, 0, false
			}
		}
		return start, count, true
	}
	oldStart, oldCount, ok = parse(fields[0][1:])
	if !ok {
		return 0, 0, 0, false
	}
	_, newCount, ok = parse(fields[1][1:])
	if !ok {
		return 0, 0, 0, false
	}
	return oldStart, oldCount, newCount, true
}

func parseNonNegativeInt(text string) (int, error) {
	if text == "" {
		return 0, fmt.Errorf("empty number")
	}
	value := 0
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", text)
		}
		value = value*10 + int(r-'0')
		if value > 1<<30 {
			return 0, fmt.Errorf("number too large: %q", text)
		}
	}
	return value, nil
}
