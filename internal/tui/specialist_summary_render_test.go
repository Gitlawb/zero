package tui

import (
	"strings"
	"testing"
	"time"
)

func TestTranscriptBodyItemsIncludesSummaryBeforeCards(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 80
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	// Add two specialist rows to the transcript.
	m.transcript = append(m.transcript, transcriptRow{
		kind:           rowSpecialist,
		specialistInfo: &specialistInfo{name: "worker", description: "fix tests", childSessionID: "s1", status: specialistRunning, startedAt: now, tokenCount: 1000},
	})
	m.transcript = append(m.transcript, transcriptRow{
		kind:           rowSpecialist,
		specialistInfo: &specialistInfo{name: "explorer", description: "map code", childSessionID: "s2", status: specialistCompleted, startedAt: now, completedAt: now, tokenCount: 2000},
	})

	items := m.transcriptBodyItems(80, "")
	body := bodyItemsString(m, items, 80)
	if !strings.Contains(body, "specialists") {
		t.Errorf("transcript body should contain summary line with 'specialists', got:\n%s", body)
	}
	if !strings.Contains(body, "3,000") {
		t.Errorf("summary should show total tokens 3,000, got:\n%s", body)
	}
}

// bodyItemsString renders body items into a single string for assertion.
func bodyItemsString(m model, items []transcriptBodyItem, width int) string {
	var lines []string
	for _, item := range items {
		rendered := item.render(0)
		lines = append(lines, rendered.lines...)
	}
	return strings.Join(lines, "\n")
}
