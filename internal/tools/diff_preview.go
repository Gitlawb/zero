package tools

import (
	"unicode/utf8"

	udiff "github.com/aymanbagabas/go-udiff"
)

// maxToolPreviewBytes caps the inline diff a write tool appends to its result, so
// a large generated file can't flood the transcript or balloon the persisted
// session events. Past this the tool falls back to its summary line alone.
const maxToolPreviewBytes = 48 * 1024

// FileDiff is a human-facing before/after file change. Registry-boundary
// redaction applies to both sides before any caller receives it.
type FileDiff struct {
	Path    string
	OldText string
	NewText string
}

// boundedFileDiff declines rather than truncating: a truncated side would look
// like an exact file replacement. Callers keep ChangedFiles as the safe
// fallback for large or unchanged content.
func boundedFileDiff(path, oldText, newText string) (FileDiff, bool) {
	if path == "" || oldText == newText || !utf8.ValidString(oldText) || !utf8.ValidString(newText) || len(oldText)+len(newText) > maxToolPreviewBytes {
		return FileDiff{}, false
	}
	return FileDiff{Path: path, OldText: oldText, NewText: newText}, true
}

// boundedUnifiedDiff returns a unified diff of oldContent -> newContent labelled
// with path, suitable for the TUI's diff card renderer. A create (oldContent "")
// yields an all-additions (green) preview; an overwrite/edit yields red/green.
// Returns "" when there is no change or the diff exceeds maxToolPreviewBytes.
func boundedUnifiedDiff(path, oldContent, newContent string) string {
	diff := udiff.Unified(path, path, oldContent, newContent)
	if diff == "" || len(diff) > maxToolPreviewBytes {
		return ""
	}
	return diff
}
