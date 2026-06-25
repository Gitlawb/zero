package tools

import (
	"fmt"
	"strings"
)

// previewBodyLines caps a card-preview diff to a glanceable head; the remainder
// is summarized with a "… +N lines" trailer. The preview is card-only (Display.
// Preview) — the model never sees it — so this bound is purely about readability.
const previewBodyLines = 15

// capPreviewDiff caps an already-formed unified diff (e.g. an apply_patch payload,
// which the model supplies pre-diffed) to a glanceable head, appending "… +N
// lines" when truncated. Writes/edits synthesize their diffs via boundedUnifiedDiff.
func capPreviewDiff(diff string) string {
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	const headCap = previewBodyLines + 4 // headers + a hunk of body
	if len(lines) <= headCap {
		return strings.Join(lines, "\n")
	}
	out := append([]string{}, lines[:headCap]...)
	return strings.Join(append(out, fmt.Sprintf("… +%d lines", len(lines)-headCap)), "\n")
}
