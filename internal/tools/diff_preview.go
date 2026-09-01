package tools

import (
	"path/filepath"
	"unicode"
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
// tabs, and ASCII spaces are normal text. Other controls, Unicode format
// characters, and non-ASCII whitespace are rejected rather than normalized, so
// invisible separators cannot split a secret before transcript redaction.
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
	if !utf8.ValidString(text) {
		return true
	}
	for _, r := range text {
		switch r {
		case '\n', '\r', '\t', ' ':
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// boundedUnifiedDiff returns a unified diff of oldContent -> newContent labelled
// with path, suitable for the TUI's diff card renderer. A create (oldContent "")
// yields an all-additions (green) preview; an overwrite/edit yields red/green.
// Returns "" when there is no change, the rendered diff is unsafe text, or the
// diff exceeds maxToolPreviewBytes. This applies the same unsafe-text gate as
// FileDiff to what reaches Display.Preview, without discarding a safe hunk only
// because an unrelated part of the source file contains an unsafe byte.
func boundedUnifiedDiff(path, oldContent, newContent string) string {
	diff := udiff.Unified(path, path, oldContent, newContent)
	if diff == "" || !utf8.ValidString(diff) || unsafeDiffText(diff) || len(diff) > maxToolPreviewBytes {
		return ""
	}
	return diff
}
