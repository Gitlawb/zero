package tools

import udiff "github.com/aymanbagabas/go-udiff"

// maxToolPreviewBytes caps the inline diff a write tool appends to its result, so
// a large generated file can't flood the transcript or balloon the persisted
// session events. Past this the tool falls back to its summary line alone.
const maxToolPreviewBytes = 48 * 1024

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
