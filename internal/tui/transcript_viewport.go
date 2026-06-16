package tui

type transcriptViewport struct {
	totalLines int
	height     int
	offset     int
}

type transcriptViewportWindow struct {
	start     int
	end       int
	height    int
	maxOffset int
	offset    int
}

func newTranscriptViewport(totalLines int, height int, offset int) transcriptViewport {
	totalLines = maxInt(0, totalLines)
	height = maxInt(1, height)
	maxOffset := maxInt(0, totalLines-height)
	return transcriptViewport{
		totalLines: totalLines,
		height:     height,
		offset:     clampInt(offset, 0, maxOffset),
	}
}

func transcriptViewportForBody(body string, frame transcriptFrameLayout, offset int) transcriptViewport {
	return newTranscriptViewport(len(viewLines(body)), frame.bodyRect.height, offset)
}

func transcriptViewportForLayout(layout transcriptBodyLayout, frame transcriptFrameLayout, offset int) transcriptViewport {
	return newTranscriptViewport(layout.totalLines(), frame.bodyRect.height, offset)
}

func (v transcriptViewport) maxOffset() int {
	return maxInt(0, v.totalLines-v.height)
}

func (v transcriptViewport) scroll(delta int) transcriptViewport {
	v.offset = clampInt(v.offset+delta, 0, v.maxOffset())
	return v
}

func (v transcriptViewport) window() transcriptViewportWindow {
	maxOffset := v.maxOffset()
	offset := clampInt(v.offset, 0, maxOffset)
	start := maxInt(0, v.totalLines-v.height-offset)
	end := minInt(v.totalLines, start+v.height)
	return transcriptViewportWindow{
		start:     start,
		end:       end,
		height:    v.height,
		maxOffset: maxOffset,
		offset:    offset,
	}
}
