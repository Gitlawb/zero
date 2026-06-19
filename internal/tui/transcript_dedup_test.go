package tui

import "testing"

// appendTranscriptRowsDedup must produce byte-identical output to repeated
// appendTranscriptRow (the O(n²) form it replaces), including keyed-row dedup and
// always-append for unkeyed rows.
func TestAppendTranscriptRowsDedupMatchesPerRow(t *testing.T) {
	newRows := []transcriptRow{
		{kind: rowToolCall, runID: 1, id: "a"},
		{kind: rowSystem, text: "note"},
		{kind: rowToolCall, runID: 1, id: "a"}, // duplicate keyed row -> skipped
		{kind: rowToolResult, runID: 1, id: "b"},
		{kind: rowSystem, text: "note"},        // unkeyed duplicate -> still appended
		{kind: rowToolCall, runID: 2, id: "a"}, // same id, different run -> distinct key
	}
	base := initialTranscript()

	want := append([]transcriptRow{}, base...)
	for _, r := range newRows {
		want = appendTranscriptRow(want, r)
	}
	got := appendTranscriptRowsDedup(append([]transcriptRow{}, base...), newRows)

	if len(got) != len(want) {
		t.Fatalf("length mismatch: bulk=%d per-row=%d", len(got), len(want))
	}
	for i := range want {
		if got[i].kind != want[i].kind || got[i].id != want[i].id || got[i].runID != want[i].runID || got[i].text != want[i].text {
			t.Fatalf("row %d differs:\n bulk=%+v\n per =%+v", i, got[i], want[i])
		}
	}
}
