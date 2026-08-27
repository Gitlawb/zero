package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

const cardNotice = "denyRead is configured, so the Windows sandbox uses the token shape without WRITE_RESTRICTED (#869)"

func resultWithPreviewAndNotice() agent.ToolResult {
	return agent.ToolResult{
		ToolCallID:         "call-1",
		Name:               "edit_file",
		Status:             tools.StatusOK,
		Output:             "Successfully edited x.go (replaced 1 occurrence).",
		Display:            tools.Display{Summary: "Successfully edited x.go.", Kind: "file", Preview: "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new"},
		EnforcementNotices: []string{cardNotice},
	}
}

func renderedCard(row transcriptRow, expanded bool) string {
	row.expanded = expanded
	return renderToolResultCard(row, 100, rowContext{}, cardRenderOptions{bodyCap: 20})
}

func rowForResult(result agent.ToolResult) transcriptRow {
	return transcriptRow{
		kind: rowToolResult, id: "r1", tool: result.Name, status: result.Status,
		text: toolResultRowText(result), detail: toolResultDetail(result),
		enforcementNotices: result.EnforcementNotices,
	}
}

// THE DISCLOSURE HAS TO REACH THE CARD, NOT JUST THE ROW TEXT.
//
// The notice is prepended to ModelOutput, which toolResultRowText carries into
// row.text, and toolCardHead is handed row.text. But the head renders the action
// and target, so the notice went nowhere. Every result with a rich preview (each
// edit and write card) rendered with no disclosure at all.
//
// Collapsed as well as expanded: a trade the operator has to expand a card to
// discover has not been disclosed.
func TestToolCardShowsTheEnforcementDisclosure(t *testing.T) {
	row := rowForResult(resultWithPreviewAndNotice())

	for _, expanded := range []bool{false, true} {
		card := renderedCard(row, expanded)
		if !strings.Contains(card, "WRITE_RESTRICTED") {
			t.Errorf("expanded=%v: the card rendered no enforcement disclosure:\n%s", expanded, card)
		}
	}
}

// THE DISCLOSURE IS DATA, NOT PROSE GLUED ONTO THE DIFF.
//
// row.detail is parsed as a diff by the files panel (planDiffStat,
// perFileDiffStats) and rendered line by line by the file view. Prefixing it
// with the notice would have been the shorter fix and would have corrupted both,
// so the notice travels in its own field and the diff stays a diff.
func TestTheDisclosureDoesNotContaminateTheDiffDetail(t *testing.T) {
	result := resultWithPreviewAndNotice()
	row := rowForResult(result)

	if strings.Contains(row.detail, "WRITE_RESTRICTED") {
		t.Fatalf("the notice leaked into the diff detail, which is parsed as a diff: %q", row.detail)
	}
	adds, dels := planDiffStat(row.detail)
	if adds != 1 || dels != 1 {
		t.Errorf("diff stats changed with the disclosure attached: +%d -%d, want +1 -1", adds, dels)
	}
}

// AND IT HAS TO SURVIVE A RESUME.
//
// The session payload carried the notice only inside the "output" string. The
// rich card is rebuilt from displayPreview, which never had it, so a restored
// transcript lost the disclosure even though the row it replaced had shown it.
func TestRestoredSessionKeepsTheEnforcementDisclosure(t *testing.T) {
	encoded, err := json.Marshal(toolResultSessionPayload(resultWithPreviewAndNotice()))
	if err != nil {
		t.Fatalf("marshal session payload: %v", err)
	}

	rows := transcriptRowsFromSessionEvents([]sessions.Event{{Type: sessions.EventToolResult, Payload: json.RawMessage(encoded)}})
	if len(rows) != 1 {
		t.Fatalf("expected one restored row, got %d", len(rows))
	}
	if len(rows[0].enforcementNotices) == 0 {
		t.Fatal("the restored row carries no enforcement notices, so the resumed transcript lost the disclosure")
	}
	for _, expanded := range []bool{false, true} {
		if card := renderedCard(rows[0], expanded); !strings.Contains(card, "WRITE_RESTRICTED") {
			t.Errorf("expanded=%v: the restored card rendered no disclosure:\n%s", expanded, card)
		}
	}
}

// A result with no notice must not grow card furniture, or every card gains a
// blank line and the disclosure stops standing out.
func TestOrdinaryResultsGainNoNoticeLines(t *testing.T) {
	result := resultWithPreviewAndNotice()
	result.EnforcementNotices = nil
	plain := renderedCard(rowForResult(result), true)

	result.EnforcementNotices = []string{"", "   "}
	blank := renderedCard(rowForResult(result), true)

	if plain != blank {
		t.Errorf("blank notices changed the card:\n--- none ---\n%s\n--- blank ---\n%s", plain, blank)
	}
}
