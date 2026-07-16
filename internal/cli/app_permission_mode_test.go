package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
)

// TestResolveDefaultPermissionMode covers the persisted defaultPermissionMode
// preference mapping, including the yolo ("unsafe") value and the safe fallback.
func TestResolveDefaultPermissionMode(t *testing.T) {
	cases := []struct {
		preference string
		want       agent.PermissionMode
		wantWarn   bool
	}{
		{"", agent.PermissionModeAsk, false},
		{"ask", agent.PermissionModeAsk, false},
		{"  ASK  ", agent.PermissionModeAsk, false},
		{"auto", agent.PermissionModeAuto, false},
		{"Auto", agent.PermissionModeAuto, false},
		{"unsafe", agent.PermissionModeUnsafe, false},
		{"UNSAFE", agent.PermissionModeUnsafe, false},
		{"yolo", agent.PermissionModeAsk, true},        // unknown → safe fallback + warn
		{"spec-draft", agent.PermissionModeAsk, true},  // internal mode not settable
		{"member-auto", agent.PermissionModeAsk, true}, // internal mode not settable
	}
	for _, c := range cases {
		var warn bytes.Buffer
		got := resolveDefaultPermissionMode(c.preference, &warn)
		if got != c.want {
			t.Errorf("resolveDefaultPermissionMode(%q) = %q, want %q", c.preference, got, c.want)
		}
		hasWarn := strings.Contains(warn.String(), "defaultPermissionMode")
		if hasWarn != c.wantWarn {
			t.Errorf("resolveDefaultPermissionMode(%q) warn = %v, want %v (stderr=%q)", c.preference, hasWarn, c.wantWarn, warn.String())
		}
	}
}
