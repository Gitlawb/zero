package tui

import "testing"

func TestTranscriptBodyItemsRepresentEmptyState(t *testing.T) {
	m := mouseTestModel()
	width := chatWidth(m.width)

	items := m.transcriptBodyItems(width, "")
	layout := layoutTranscriptBodyItems(items)

	if len(layout.spans) != 1 || layout.spans[0].kind != transcriptBodyItemEmpty {
		t.Fatalf("spans = %#v, want one empty-state item", layout.spans)
	}
	if layout.spans[0].height != len(layout.lines) || layout.spans[0].height == 0 {
		t.Fatalf("empty-state span = %#v lines=%d, want positive span covering all lines", layout.spans[0], len(layout.lines))
	}
	if len(layout.selectable) != 0 {
		t.Fatalf("empty state should not expose selectable transcript text: %#v", layout.selectable)
	}
}

func TestTranscriptBodyItemsShiftSelectableLinesByItemStart(t *testing.T) {
	m := mouseTestModel()
	m.transcript = appendRow(m.transcript, rowUser, "hello")
	width := chatWidth(m.width)

	layout := layoutTranscriptBodyItems(m.transcriptBodyItems(width, ""))

	if len(layout.spans) != 1 || layout.spans[0].kind != transcriptBodyItemRow {
		t.Fatalf("spans = %#v, want one transcript row item", layout.spans)
	}
	rowSpan := layout.spans[0]
	if rowSpan.height != 3 {
		t.Fatalf("user row height = %d, want padding/text/padding", rowSpan.height)
	}
	if len(layout.selectable) != 1 {
		t.Fatalf("selectable lines = %#v, want one user text line", layout.selectable)
	}
	if got, want := layout.selectable[0].bodyY, rowSpan.startY+1; got != want {
		t.Fatalf("selectable bodyY = %d, want item start + user padding = %d", got, want)
	}
	if layout.selectable[0].rowIndex != len(m.transcript)-1 || layout.selectable[0].text != "hello" {
		t.Fatalf("selectable line = %#v, want user row text", layout.selectable[0])
	}
}

func TestTranscriptBodyItemsKeepPendingInterimSelectableLocal(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.streamingReasoning = "private thought"
	width := chatWidth(m.width)

	layout := layoutTranscriptBodyItems(m.transcriptBodyItems(width, ""))

	if len(layout.spans) != 2 {
		t.Fatalf("spans = %#v, want separator plus pending interim", layout.spans)
	}
	if layout.spans[0].kind != transcriptBodyItemSeparator || layout.spans[0].height != 1 {
		t.Fatalf("first span = %#v, want one-line separator", layout.spans[0])
	}
	pendingSpan := layout.spans[1]
	if pendingSpan.kind != transcriptBodyItemPendingInterim || pendingSpan.height == 0 {
		t.Fatalf("pending span = %#v, want rendered interim item", pendingSpan)
	}
	if len(layout.selectable) != 1 || !layout.selectable[0].live || !layout.selectable[0].toggle {
		t.Fatalf("selectable lines = %#v, want live streaming reasoning toggle", layout.selectable)
	}
	if layout.selectable[0].bodyY != pendingSpan.startY {
		t.Fatalf("streaming selectable bodyY = %d, want pending item start %d", layout.selectable[0].bodyY, pendingSpan.startY)
	}
}

func TestTranscriptBodyLayoutVisibleLinesUsesViewportWindow(t *testing.T) {
	layout := transcriptBodyLayout{lines: []string{"zero", "one", "two", "three"}}
	window := newTranscriptViewport(layout.totalLines(), 2, 1).window()

	got := layout.visibleLines(window)
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("visible lines = %#v, want [one two]", got)
	}
	got[0] = "mutated"
	if layout.lines[1] != "one" {
		t.Fatalf("visibleLines should return a copy, layout lines = %#v", layout.lines)
	}
}
