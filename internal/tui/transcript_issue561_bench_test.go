package tui

import (
	"fmt"
	"testing"
)

func benchmarkIssue561Model(turns int) model {
	m := transcriptViewTestModel()
	m.altScreen = true
	for i := 0; i < turns; i++ {
		m.transcript = append(m.transcript,
			transcriptRow{kind: rowUser, text: fmt.Sprintf("question %d", i)},
			transcriptRow{kind: rowAssistant, text: fmt.Sprintf("answer %d", i), final: true},
		)
	}
	m, _ = m.settleTranscript()
	return m
}

func BenchmarkIssue561SettledAltScreen(b *testing.B) {
	m := benchmarkIssue561Model(5000)
	width := m.chatColumnWidth()
	if m.altScreenSettledFrontier != m.flushed || m.altScreenSettledWidth != width {
		b.Fatalf(
			"settled cache miss before benchmark: frontier=%d flushed=%d width=%d wantWidth=%d",
			m.altScreenSettledFrontier,
			m.flushed,
			m.altScreenSettledWidth,
			width,
		)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.transcriptBodyItems(width, "", false)
	}
}
