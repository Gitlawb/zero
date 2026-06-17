package tui

import "testing"

func TestTranscriptViewportWindowIsBottomAnchored(t *testing.T) {
	viewport := newTranscriptViewport(20, 5, 0)
	window := viewport.window()

	if window.start != 15 || window.end != 20 || window.height != 5 || window.maxOffset != 15 {
		t.Fatalf("window = %#v, want bottom five lines with max offset 15", window)
	}
}

func TestTranscriptViewportScrollClamps(t *testing.T) {
	viewport := newTranscriptViewport(20, 5, 0)

	viewport = viewport.scroll(100)
	if viewport.offset != 15 {
		t.Fatalf("scroll past top offset = %d, want 15", viewport.offset)
	}
	viewport = viewport.scroll(-100)
	if viewport.offset != 0 {
		t.Fatalf("scroll past bottom offset = %d, want 0", viewport.offset)
	}
}

func TestTranscriptViewportKeepsNonScrollableOffsetAtZero(t *testing.T) {
	viewport := newTranscriptViewport(3, 10, 99)
	window := viewport.window()

	if viewport.offset != 0 || window.start != 0 || window.end != 3 || window.maxOffset != 0 {
		t.Fatalf("viewport=%#v window=%#v, want non-scrollable viewport clamped to full body", viewport, window)
	}
}

func TestTranscriptViewportHandlesZeroAndNegativeInputs(t *testing.T) {
	for _, tt := range []struct {
		name       string
		totalLines int
		height     int
		offset     int
		want       transcriptViewportWindow
	}{
		{
			name:       "zero lines",
			totalLines: 0,
			height:     5,
			offset:     10,
			want:       transcriptViewportWindow{start: 0, end: 0, height: 5, maxOffset: 0, offset: 0},
		},
		{
			name:       "zero height",
			totalLines: 3,
			height:     0,
			offset:     2,
			want:       transcriptViewportWindow{start: 0, end: 1, height: 1, maxOffset: 2, offset: 2},
		},
		{
			name:       "negative values",
			totalLines: -10,
			height:     -4,
			offset:     -20,
			want:       transcriptViewportWindow{start: 0, end: 0, height: 1, maxOffset: 0, offset: 0},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			window := newTranscriptViewport(tt.totalLines, tt.height, tt.offset).window()
			if window != tt.want {
				t.Fatalf("window = %#v, want %#v", window, tt.want)
			}
		})
	}
}

func TestTranscriptViewportClampsInflatedOffsetBeforeScrollingDown(t *testing.T) {
	viewport := newTranscriptViewport(20, 5, 100)
	if viewport.offset != 15 {
		t.Fatalf("inflated offset should clamp to top, got %d", viewport.offset)
	}

	viewport = viewport.scroll(-5)
	if viewport.offset != 10 {
		t.Fatalf("scroll down from clamped top offset = %d, want 10", viewport.offset)
	}
}
