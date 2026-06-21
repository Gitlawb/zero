package tools

import (
	"fmt"
	"strings"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// maxPreviewLines caps a UI preview/diff body so a huge file or rewrite doesn't
// produce a multi-thousand-line render payload. The TUI caps again per its width
// tier; this is the upstream guard, and it never affects the model-facing Output.
const maxPreviewLines = 500

// unifiedDiff returns a unified diff of oldContent -> newContent labeled with the
// workspace-relative path, for the TUI to render (Display.Body with Kind "diff").
// Returns "" when the contents are identical.
func unifiedDiff(relativePath, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	edits := myers.ComputeEdits(span.URIFromPath(relativePath), oldContent, newContent)
	if len(edits) == 0 {
		return ""
	}
	diff := fmt.Sprint(gotextdiff.ToUnified("a/"+relativePath, "b/"+relativePath, oldContent, edits))
	return capPreview(diff)
}

// WrittenContentPreview returns an all-added diff of content, for the TUI to
// preview a freshly written file when no prior content is available — e.g. an
// external MCP file-write tool, where Zero never saw the old bytes. Returns ""
// for empty content.
func WrittenContentPreview(relativePath, content string) string {
	return unifiedDiff(relativePath, "", content)
}

// capPreview trims a render body to maxPreviewLines, appending a hidden-count
// trailer when it overflows so the UI never renders an enormous payload.
func capPreview(body string) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= maxPreviewLines {
		return body
	}
	hidden := len(lines) - maxPreviewLines
	kept := make([]string, 0, maxPreviewLines+1)
	kept = append(kept, lines[:maxPreviewLines]...)
	kept = append(kept, fmt.Sprintf("… %d more lines", hidden))
	return strings.Join(kept, "\n")
}
