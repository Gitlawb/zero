package tools

import (
	"path/filepath"
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
	// Path is the canonical absolute path required by ACP diff content. The
	// separate ChangedFiles result remains workspace-relative for local UI use.
	Path string
	// OldExists and NewExists distinguish an empty file from a missing side of a
	// create/delete/move. Empty strings alone cannot encode that difference.
	OldExists bool
	NewExists bool
	OldText   string
	NewText   string
}

// boundedFileDiff declines rather than truncating: a truncated side would look
// like an exact file replacement. Callers keep ChangedFiles as the safe
// fallback for large, unsafe, or unchanged content. Newlines, carriage returns,
// and tabs are normal text; the remaining C0/C1 controls are rejected rather
// than normalized, so they cannot split a secret before transcript redaction.
func boundedFileDiff(path, oldText, newText string, oldExists, newExists bool) (FileDiff, bool) {
	if !filepath.IsAbs(path) || (!oldExists && !newExists) ||
		(oldExists == newExists && oldText == newText) ||
		!utf8.ValidString(oldText) || !utf8.ValidString(newText) ||
		unsafeDiffText(oldText) || unsafeDiffText(newText) ||
		len(oldText)+len(newText) > maxToolPreviewBytes {
		return FileDiff{}, false
	}
	return FileDiff{Path: path, OldExists: oldExists, NewExists: newExists, OldText: oldText, NewText: newText}, true
}

func unsafeDiffText(text string) bool {
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
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
