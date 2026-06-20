package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// streamingTailLines is how many trailing lines of a file's in-progress content
// the live "writing" block shows — enough to watch the code flow without taking
// over the screen.
const streamingTailLines = 6

// isFileWritingTool reports whether a tool's streamed arguments are worth showing
// as live code (a file being written or edited).
func isFileWritingTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "apply_patch":
		return true
	}
	return false
}

// decodeStreamingJSONString extracts the (possibly unterminated) value of a
// top-level string field from a streaming JSON args buffer — used to pull the
// path and the file content out of a tool call as its arguments arrive. Best
// effort: it unescapes \n \t \" \\ \/ for a readable preview, skips \uXXXX, and
// stops at the closing quote or the stream edge (a dangling backslash is dropped).
func decodeStreamingJSONString(args, key string) (string, bool) {
	// Find `"key"`, then tolerate optional whitespace around the colon and before
	// the opening quote, so both `"key":"v"` and `"key": "v"` (model JSON formatting
	// varies — kimi spaces after the colon) parse.
	keyMarker := `"` + key + `"`
	idx := strings.Index(args, keyMarker)
	if idx < 0 {
		return "", false
	}
	i := skipJSONSpace(args, idx+len(keyMarker))
	if i >= len(args) || args[i] != ':' {
		return "", false
	}
	i = skipJSONSpace(args, i+1)
	if i >= len(args) || args[i] != '"' {
		return "", false // value isn't a string yet (or hasn't streamed in)
	}
	rest := args[i+1:]
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '\\' {
			if i+1 >= len(rest) {
				break // incomplete escape at the stream edge
			}
			i++
			switch rest[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				// drop carriage returns from the preview
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case '/':
				b.WriteByte('/')
			case 'u':
				if i+4 < len(rest) {
					i += 4 // skip the 4 hex digits; the exact rune doesn't matter for a preview
				} else {
					i = len(rest)
				}
			default:
				b.WriteByte(rest[i])
			}
			continue
		}
		if c == '"' {
			break // closing quote
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// skipJSONSpace advances past JSON insignificant whitespace.
func skipJSONSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// streamingFilePath pulls the file path out of a streaming tool-call args buffer,
// trying the argument names the file tools use. Used to seed the decoder's path.
func streamingFilePath(args string) string {
	for _, key := range []string{"path", "file_path", "filename"} {
		if v, ok := decodeStreamingJSONString(args, key); ok && v != "" {
			return v
		}
	}
	return ""
}

// streamingToolCallView renders the in-progress file-writing tool call — its path,
// a live line count, and a tail of the streaming content — so a long write/edit
// shows the code flowing in instead of a frozen spinner. It reads the decoder's
// incrementally-maintained state (O(1) per render); returns "" when no
// file-writing call is mid-stream.
func (m model) streamingToolCallView(width int) string {
	if m.streamCallID == "" || !isFileWritingTool(m.streamCallName) || m.streamCallDecoder == nil {
		return ""
	}
	d := m.streamCallDecoder

	head := zeroTheme.accent.Render("✎ ") + zeroTheme.toolName.Render(m.streamCallName)
	if d.path != "" {
		head += " " + zeroTheme.toolTarget.Render(d.path)
	}
	switch {
	case d.hasContent():
		head += zeroTheme.faint.Render(fmt.Sprintf("  ·  %d lines", d.lineTotal()))
	case d.rawLen > 0:
		// Args are streaming but the content field hasn't arrived yet — show a live
		// byte count so it reads as progressing, never frozen.
		head += zeroTheme.faint.Render(fmt.Sprintf("  ·  receiving %.1f KB", float64(d.rawLen)/1024))
	}
	lines := []string{head}
	if d.hasContent() {
		bodyWidth := maxInt(8, width-4)
		// write_file/edit_file content is brand-new, so every line is an addition;
		// apply_patch carries a real ± diff. styleStreamingCodeLine colors added
		// lines green, removed red, and everything else bright (not the old dim gray).
		newContent := m.streamCallName == "write_file" || m.streamCallName == "edit_file"
		for _, line := range d.tailLines() {
			line = strings.ReplaceAll(line, "\t", "    ")
			// Truncate by display WIDTH (ansi.Truncate), not rune count, so wide/CJK
			// glyphs can't overrun bodyWidth and break the tail layout.
			lines = append(lines, "  "+styleStreamingCodeLine(ansi.Truncate(line, bodyWidth, ""), newContent))
		}
	}
	return strings.Join(lines, "\n")
}

// styleStreamingCodeLine colors one line of the live code preview: explicit diff
// additions (+) render green and removals (-) red; for a brand-new write_file/edit
// every line is an addition (green); anything else renders in bright ink. This
// replaces the old dim faintest gray the streamed code was nearly invisible in.
func styleStreamingCodeLine(line string, newContent bool) string {
	// A brand-new write_file/edit_file is NOT a diff: every line is added content,
	// so color it all green and never treat a leading "-" (e.g. CSS "-webkit-…") as
	// a removal. Only apply_patch (newContent=false) carries real ± diff markers.
	if newContent {
		return zeroTheme.green.Render(line)
	}
	trimmed := strings.TrimLeft(line, " ")
	switch {
	case strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "+++"):
		return zeroTheme.green.Render(line)
	case strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "---"):
		return zeroTheme.red.Render(line)
	default:
		return zeroTheme.ink.Render(line)
	}
}
