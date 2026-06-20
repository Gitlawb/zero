package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInitCommandLaunchesBootstrapRun(t *testing.T) {
	m := newModel(context.Background(), Options{Cwd: t.TempDir()})
	m.input.SetValue("/init")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	// /init launches a normal agent turn seeded with the AGENTS.md bootstrap
	// prompt; the prompt lands in the transcript as the user turn.
	if !transcriptContains(next.transcript, "Generate an AGENTS.md") {
		t.Fatalf("/init should launch the bootstrap run, got %#v", next.transcript)
	}
}
