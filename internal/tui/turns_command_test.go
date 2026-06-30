package tui

import (
	"strings"
	"testing"
)

func TestHandleTurnsCommand(t *testing.T) {
	t.Run("sets a valid budget", func(t *testing.T) {
		got, out := model{}.handleTurnsCommand("150")
		if got.agentOptions.MaxTurns != 150 {
			t.Fatalf("MaxTurns = %d, want 150", got.agentOptions.MaxTurns)
		}
		if !strings.Contains(out, "150") {
			t.Fatalf("output should report 150, got %q", out)
		}
	})

	t.Run("clamps above the ceiling", func(t *testing.T) {
		got, _ := model{}.handleTurnsCommand("99999")
		if got.agentOptions.MaxTurns != maxTurnsCeiling {
			t.Fatalf("MaxTurns = %d, want clamp to %d", got.agentOptions.MaxTurns, maxTurnsCeiling)
		}
	})

	t.Run("invalid / non-positive input is rejected without changing the budget", func(t *testing.T) {
		for _, in := range []string{"abc", "0", "-5", "3.5"} {
			m := model{}
			m.agentOptions.MaxTurns = 42
			got, out := m.handleTurnsCommand(in)
			if got.agentOptions.MaxTurns != 42 {
				t.Fatalf("input %q changed MaxTurns to %d, want unchanged (42)", in, got.agentOptions.MaxTurns)
			}
			if !strings.Contains(out, "Usage") {
				t.Fatalf("input %q should show usage, got %q", in, out)
			}
		}
	})

	t.Run("empty / status shows the current budget without changing it", func(t *testing.T) {
		m := model{}
		m.agentOptions.MaxTurns = 42
		got, out := m.handleTurnsCommand("")
		if got.agentOptions.MaxTurns != 42 {
			t.Fatalf("status changed MaxTurns to %d, want 42", got.agentOptions.MaxTurns)
		}
		if !strings.Contains(out, "42") {
			t.Fatalf("status should report current 42, got %q", out)
		}
	})
}
