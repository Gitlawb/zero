package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSandboxSetupCommandRunsAsync(t *testing.T) {
	called := false
	m := newModel(context.Background(), Options{
		SandboxSetupCommand: func(context.Context) SandboxSetupCommandResult {
			called = true
			return SandboxSetupCommandResult{
				Output:   "Windows sandbox setup complete",
				ExitCode: 0,
			}
		},
	})
	m.input.SetValue("/sandbox-setup")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected /sandbox-setup to return an async command")
	}
	if called {
		t.Fatal("sandbox setup ran synchronously before the returned command executed")
	}
	if !transcriptContains(next.transcript, "Running native sandbox setup") {
		t.Fatalf("expected running setup status, got %#v", next.transcript)
	}

	updated, _ = next.Update(execCmd(cmd))
	final := updated.(model)
	if !called {
		t.Fatal("sandbox setup did not run when the async command executed")
	}
	for _, want := range []string{"Sandbox setup", "status: ok", "Windows sandbox setup complete"} {
		if !transcriptContains(final.transcript, want) {
			t.Fatalf("expected final setup transcript to contain %q, got %#v", want, final.transcript)
		}
	}
}

func TestSandboxSetupCommandReportsUnavailableRunner(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/sandbox-setup")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected unavailable /sandbox-setup to be handled synchronously")
	}
	if !transcriptContains(next.transcript, "Sandbox setup is not available") {
		t.Fatalf("expected unavailable setup message, got %#v", next.transcript)
	}
}

func TestSandboxSetupCommandRejectsArgs(t *testing.T) {
	m := newModel(context.Background(), Options{
		SandboxSetupCommand: func(context.Context) SandboxSetupCommandResult {
			t.Fatal("sandbox setup should not run when args are present")
			return SandboxSetupCommandResult{}
		},
	})
	m.input.SetValue("/sandbox-setup now")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected invalid /sandbox-setup to be handled synchronously")
	}
	if !transcriptContains(next.transcript, "Usage: /sandbox-setup") {
		t.Fatalf("expected setup usage, got %#v", next.transcript)
	}
}

func TestSandboxSetupCommandIsDiscoverable(t *testing.T) {
	names := listCommandNames()
	found := false
	for _, name := range names {
		if name == "/sandbox-setup" {
			found = true
		}
	}
	if !found {
		t.Fatalf("/sandbox-setup should be listed so it appears in help and autocomplete: %#v", names)
	}
}

func TestSandboxSetupCommandAutocompleteIsGated(t *testing.T) {
	withoutRunner := newModel(context.Background(), Options{})
	withoutRunner.input.SetValue("/sandbox")
	withoutRunner.recomputeSuggestions()
	if commandSuggestionNamesContain(withoutRunner.suggestions, "/sandbox-setup") {
		t.Fatalf("default autocomplete should hide /sandbox-setup, got %#v", withoutRunner.suggestions)
	}

	withRunner := newModel(context.Background(), Options{
		SandboxSetupCommand: func(context.Context) SandboxSetupCommandResult {
			return SandboxSetupCommandResult{ExitCode: 0}
		},
	})
	withRunner.input.SetValue("/sandbox")
	withRunner.recomputeSuggestions()
	if !commandSuggestionNamesContain(withRunner.suggestions, "/sandbox-setup") {
		t.Fatalf("setup-capable autocomplete should show /sandbox-setup, got %#v", withRunner.suggestions)
	}
}
