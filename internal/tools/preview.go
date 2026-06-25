package tools

import (
	"fmt"
	"strings"
)

// previewBodyLines caps a card-preview diff to a glanceable head; the remainder
// is summarized with a "… +N lines" trailer. The preview is card-only (Display.
// Preview) — the model never sees it — so this bound is purely about readability.
const previewBodyLines = 15

// capDiffLines truncates a diff body to max lines, appending a plain "… +N lines"
// trailer (a context-style line, so it survives looksLikeDiff and renders muted).
func capDiffLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	out := append([]string{}, lines[:max]...)
	return append(out, fmt.Sprintf("… +%d lines", len(lines)-max))
}

// newFileDiffPreview synthesizes an all-additions unified diff for a freshly
// written file — the first previewBodyLines content lines as "+". existed=false
// renders as a true create (--- /dev/null) so the card shows a NEW FILE badge.
func newFileDiffPreview(path, content string, lines int, existed bool) string {
	from := "--- a/" + path
	if !existed {
		from = "--- /dev/null"
	}
	header := []string{from, "+++ b/" + path, fmt.Sprintf("@@ -0,0 +1,%d @@", lines)}
	body := make([]string, 0, lines)
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		body = append(body, "+"+line)
	}
	return strings.Join(append(header, capDiffLines(body, previewBodyLines)...), "\n")
}

// editDiffPreview builds a small unified-diff hunk for an edit_file replacement:
// the old block as "-" lines, the new block as "+" lines, located at the first
// occurrence of oldString in the original content. Capped.
func editDiffPreview(path, content, oldString, newString string) string {
	start := 1
	if idx := strings.Index(content, oldString); idx >= 0 {
		start = strings.Count(content[:idx], "\n") + 1
	}
	oldLines := strings.Split(strings.TrimRight(oldString, "\n"), "\n")
	newLines := strings.Split(strings.TrimRight(newString, "\n"), "\n")
	header := []string{
		"--- a/" + path,
		"+++ b/" + path,
		fmt.Sprintf("@@ -%d,%d +%d,%d @@", start, len(oldLines), start, len(newLines)),
	}
	body := make([]string, 0, len(oldLines)+len(newLines))
	for _, l := range oldLines {
		body = append(body, "-"+l)
	}
	for _, l := range newLines {
		body = append(body, "+"+l)
	}
	return strings.Join(append(header, capDiffLines(body, previewBodyLines)...), "\n")
}

// capPreviewDiff caps an already-formed unified diff (e.g. an apply_patch payload)
// to a glanceable head, appending "… +N lines" when truncated.
func capPreviewDiff(diff string) string {
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	const headCap = previewBodyLines + 4 // headers + a hunk of body
	if len(lines) <= headCap {
		return strings.Join(lines, "\n")
	}
	out := append([]string{}, lines[:headCap]...)
	return strings.Join(append(out, fmt.Sprintf("… +%d lines", len(lines)-headCap)), "\n")
}
