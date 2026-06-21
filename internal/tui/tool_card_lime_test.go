package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Gitlawb/zero/internal/tools"
)

// TestToolCardHeadIsLimeBandFullWidth pins the Lime Refined tool-card head: the
// tool name and path render, the diff text survives, and the head row is a single
// full-width header band (rail + backed head + backed padding + status glyph). The
// band-fill math is what a stray plain separator would break.
func TestToolCardHeadIsLimeBandFullWidth(t *testing.T) {
	m := limeTestModel()
	diff := strings.Join([]string{
		"--- a/internal/auth/middleware.go",
		"+++ b/internal/auth/middleware.go",
		"@@ -1,2 +1,2 @@",
		"-    claims, err := parseV1(token)",
		"+    claims, err := parseToken(token)",
		" }",
	}, "\n")
	row := transcriptRow{kind: rowToolResult, id: "c1", tool: "edit_file", status: tools.StatusOK, detail: diff}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "c1", tool: "edit_file", detail: "internal/auth/middleware.go"}})
	const width = 80

	rendered := m.renderRow(row, width, rc)
	plain := plainRender(t, rendered)
	for _, want := range []string{"edit_file", "internal/auth/middleware.go", "parseToken", "parseV1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("tool card missing %q in:\n%s", want, plain)
		}
	}
	headLine := strings.SplitN(rendered, "\n", 2)[0]
	if w := lipgloss.Width(headLine); w != width {
		t.Fatalf("head band width = %d, want full card width %d: %q", w, width, headLine)
	}
}
