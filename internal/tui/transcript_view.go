package tui

import "strings"

func (m model) toggleDetailedTranscript() model {
	m.transcriptDetailed = !m.transcriptDetailed
	m.clearSuggestions()
	m.picker = nil
	return m
}

func (m model) detailedTranscriptView() string {
	width := chatWidth(m.width)
	rc := buildRowContext(m.transcript)
	renderer := m
	renderer.pending = false
	renderer.activeRunID = 0

	var builder strings.Builder
	builder.WriteString(detailedTranscriptHeader(width))
	builder.WriteString("\n")
	builder.WriteString(zeroTheme.line.Render(strings.Repeat("-", width)))
	builder.WriteString("\n")

	shownAny := false
	for _, row := range m.transcript {
		if rc.skip(row) {
			continue
		}
		rendered := ""
		if row.kind == rowWelcome {
			rendered = fitStyledLine(zeroTheme.faint.Render(row.text), width)
		} else {
			rendered = renderer.renderRowDetailed(row, width, rc)
		}
		if rendered == "" {
			continue
		}
		if shownAny && startsTurn(row.kind) {
			builder.WriteString("\n")
		}
		builder.WriteString(rendered)
		builder.WriteString("\n")
		shownAny = true
	}

	if !shownAny {
		builder.WriteString(zeroTheme.faint.Render("No transcript rows."))
		builder.WriteString("\n")
	}

	builder.WriteString(zeroTheme.line.Render(strings.Repeat("-", width)))
	builder.WriteString("\n")
	builder.WriteString(fitStyledLine(zeroTheme.faint.Render("Esc close | Ctrl+O toggle"), width))
	return builder.String()
}

func detailedTranscriptHeader(width int) string {
	title := zeroTheme.ink.Bold(true).Render("Transcript")
	hint := zeroTheme.faint.Render("detailed")
	return fitStyledLine(joinHeaderLine(title, hint, width), width)
}
