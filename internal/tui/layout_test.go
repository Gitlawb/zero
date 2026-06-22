package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTranscriptFrameLayoutPinsMainRegions(t *testing.T) {
	m := mouseTestModel()
	m.width = 100
	m.height = 24
	m.providerName = "openai"
	m.modelName = "gpt-4.1"

	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))

	if frame.headerRect != (tuiRect{width: width, height: len(frame.headerLines)}) {
		t.Fatalf("header rect = %#v, want y=0 width=%d height=%d", frame.headerRect, width, len(frame.headerLines))
	}
	if frame.bodyRect.y != frame.headerRect.height {
		t.Fatalf("body y = %d, want below header height %d", frame.bodyRect.y, frame.headerRect.height)
	}
	if frame.footerRect.y != frame.bodyRect.y+frame.bodyRect.height {
		t.Fatalf("footer y = %d, want after body at %d", frame.footerRect.y, frame.bodyRect.y+frame.bodyRect.height)
	}
	if frame.footerRect.y+frame.footerRect.height != m.height {
		t.Fatalf("footer bottom = %d, want terminal height %d", frame.footerRect.y+frame.footerRect.height, m.height)
	}
	if frame.composerRect.height <= 0 ||
		!frame.footerRect.contains(frame.composerRect.x, frame.composerRect.y) ||
		!frame.footerRect.contains(frame.composerRect.x+frame.composerRect.width-1, frame.composerRect.y+frame.composerRect.height-1) {
		t.Fatalf("composer rect should be visible inside footer, frame=%#v", frame)
	}
	if frame.statusRect.height != 1 || frame.statusRect.y != frame.footerRect.y+frame.footerRect.height-1 {
		t.Fatalf("status rect = %#v, want final visible footer line", frame.statusRect)
	}
}

func TestTranscriptFrameLayoutClipsFooterInTinyTerminal(t *testing.T) {
	m := mouseTestModel()
	m.width = 44
	m.height = 3
	m.copyStatus = "Copied!"
	m.input.SetValue("Create a book library dashboard page with cards, filters, charts, and responsive behavior.")

	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))

	if frame.bodyRect.height != 1 {
		t.Fatalf("body height = %d, want one protected viewport line", frame.bodyRect.height)
	}
	if frame.footerRect.height != m.height-1 {
		t.Fatalf("footer height = %d, want clipped to %d", frame.footerRect.height, m.height-1)
	}
	if frame.headerRect.height != 0 {
		t.Fatalf("header should clip out before body disappears, got %#v", frame.headerRect)
	}
	if frame.footerClip <= 0 {
		t.Fatalf("footerClip = %d, want clipped full footer", frame.footerClip)
	}
	if len(frame.footerLines) >= len(frame.fullFooterLines) {
		t.Fatalf("footer lines were not clipped: visible=%d full=%d", len(frame.footerLines), len(frame.fullFooterLines))
	}
}

func TestTranscriptFrameLayoutHandlesDegenerateDimensions(t *testing.T) {
	for _, size := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "zero", width: 0, height: 0},
		{name: "negative", width: -10, height: -4},
	} {
		t.Run(size.name, func(t *testing.T) {
			m := mouseTestModel()
			m.width = size.width
			m.height = size.height

			width := m.chatColumnWidth()
			frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
			if width <= 0 {
				t.Fatalf("chatWidth(%d) = %d, want positive fallback", size.width, width)
			}
			if frame.bodyRect.height < 1 {
				t.Fatalf("body rect = %#v, want protected body height", frame.bodyRect)
			}
			if frame.width != width {
				t.Fatalf("frame width = %d, want chat width %d", frame.width, width)
			}
		})
	}
}

func TestFrameComposerRegionDrivesMouseHit(t *testing.T) {
	m := mouseTestModel()
	m.width = 44
	m.height = 20
	m.input.SetValue("Create a book library dashboard page with cards, filters, charts, and responsive behavior.")

	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	if frame.composerRect.height <= 0 {
		t.Fatalf("expected visible composer rect, frame=%#v", frame)
	}
	if !m.mouseOverComposer(testMouseWheel(tea.MouseWheelUp, 0, frame.composerRect.y)) {
		t.Fatalf("mouse over composer y=%d should hit composer rect %#v", frame.composerRect.y, frame.composerRect)
	}
	if m.mouseOverComposer(testMouseWheel(tea.MouseWheelUp, 0, frame.statusRect.y)) {
		t.Fatalf("mouse over status y=%d should not hit composer rect %#v", frame.statusRect.y, frame.composerRect)
	}
}

func TestOverlayMouseRectCentersInsideTranscriptBody(t *testing.T) {
	m := mouseTestModel()
	m.width = 100
	m.height = 30

	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	rect := m.overlayMouseRect(5, width)

	wantY := frame.bodyRect.y + (frame.bodyRect.height-5)/2
	if rect.y != wantY || rect.height != 5 || rect.width != width {
		t.Fatalf("overlay rect = %#v, want y=%d height=5 width=%d", rect, wantY, width)
	}
	if !frame.bodyRect.contains(rect.x, rect.y) || !frame.bodyRect.contains(rect.x, rect.y+rect.height-1) {
		t.Fatalf("overlay rect %#v should be inside body rect %#v", rect, frame.bodyRect)
	}
}

func TestOverlayMouseHitClampsToVisibleBody(t *testing.T) {
	m := mouseTestModel()
	m.width = 80
	m.height = 8

	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	overlay := strings.Join([]string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}, "\n")
	rect := m.overlayMouseRect(len(viewLines(overlay)), width)
	if rect.height != frame.bodyRect.height {
		t.Fatalf("overlay rect = %#v, want height clamped to body rect %#v", rect, frame.bodyRect)
	}

	if _, ok := m.overlayMouseHit(testMouseClick(tea.MouseLeft, 1, rect.y+rect.height-1), overlay, width); !ok {
		t.Fatalf("click inside visible overlay rect %#v should hit", rect)
	}
	if _, ok := m.overlayMouseHit(testMouseClick(tea.MouseLeft, 1, rect.y+rect.height), overlay, width); ok {
		t.Fatalf("click below visible overlay rect %#v should not hit invisible overlay rows", rect)
	}
}

func TestTranscriptLineAtMouseUsesFrameBodyRegion(t *testing.T) {
	m := mouseTestModel()
	m.width = 90
	m.height = 12
	m.transcript = appendRow(m.transcript, rowUser, "target text")

	width := m.chatColumnWidth()
	body, selectable := m.transcriptBody(width, "")
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	start, _, _ := transcriptViewportStartForFrame(body, frame, m.chatScrollOffset)

	var target transcriptSelectableLine
	for _, line := range selectable {
		if strings.Contains(line.text, "target text") {
			target = line
			break
		}
	}
	if target.text == "" {
		t.Fatalf("target line missing from selectable lines: %#v", selectable)
	}

	hit, ok := m.transcriptLineAtMouse(testMouseClick(tea.MouseLeft, target.textStart, frame.bodyRect.y+target.bodyY-start))
	if !ok || hit.text != target.text {
		t.Fatalf("transcript line hit = %#v ok=%v, want %#v", hit, ok, target)
	}
	if _, ok := m.transcriptLineAtMouse(testMouseClick(tea.MouseLeft, target.textStart, frame.headerRect.y)); ok {
		t.Fatal("header click should not map to transcript body")
	}
}
