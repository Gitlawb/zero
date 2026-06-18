package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
)

func pendingPermissionModel(t *testing.T, decide func(agent.PermissionDecision)) model {
	t.Helper()
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	updated, _ := m.Update(permissionRequestMsg{
		runID:   7,
		request: testPromptPermissionRequest(),
		decide:  decide,
	})
	next := updated.(model)
	if next.pendingPermission == nil {
		t.Fatal("setup: expected a pending permission prompt")
	}
	return next
}

func TestPermissionCursorDefaultsToAllowOnce(t *testing.T) {
	m := pendingPermissionModel(t, func(agent.PermissionDecision) {})
	if m.pendingPermission.cursor != 0 {
		t.Fatalf("default cursor = %d, want 0 (allow once)", m.pendingPermission.cursor)
	}
}

func TestPermissionCursorMovesAndEnterConfirms(t *testing.T) {
	decisions := []permissionDecision{}
	m := pendingPermissionModel(t, func(d agent.PermissionDecision) {
		decisions = append(decisions, permissionDecision(d.Action))
	})
	// 0 →down 1 →down 2 →up 1 (always).
	for _, key := range []rune{tea.KeyDown, tea.KeyDown, tea.KeyUp} {
		updated, _ := m.Update(testKey(key))
		m = updated.(model)
	}
	if m.pendingPermission == nil || m.pendingPermission.cursor != 1 {
		t.Fatalf("cursor after down,down,up = %v, want 1 (always)", m.pendingPermission)
	}
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if len(decisions) != 1 || decisions[0] != permissionDecisionAlwaysAllow {
		t.Fatalf("enter on cursor 1 should resolve 'always', got %#v", decisions)
	}
	if m.pendingPermission != nil {
		t.Fatal("prompt should clear after confirm")
	}
}

func TestPermissionCursorWrapsWithUp(t *testing.T) {
	m := pendingPermissionModel(t, func(agent.PermissionDecision) {})
	updated, _ := m.Update(testKey(tea.KeyUp)) // 0 wraps to last (deny)
	m = updated.(model)
	if want := len(permissionOptions()) - 1; m.pendingPermission.cursor != want {
		t.Fatalf("Up from 0 should wrap to %d, got %d", want, m.pendingPermission.cursor)
	}
}

func TestPermissionHotkeysStillResolveDirectly(t *testing.T) {
	got := []permissionDecision{}
	m := pendingPermissionModel(t, func(d agent.PermissionDecision) {
		got = append(got, permissionDecision(d.Action))
	})
	if _, cmd := m.Update(testKeyText("d")); cmd != nil { // hotkey ignores the cursor
		t.Fatal("'d' should resolve synchronously")
	}
	if len(got) != 1 || got[0] != permissionDecisionDeny {
		t.Fatalf("'d' should resolve deny directly, got %#v", got)
	}
}

func TestPermissionRenderEmitsHighlightedClickableOffsets(t *testing.T) {
	request := agent.PermissionRequest{ToolName: "bash"}
	card, offsets := renderFocusedPermissionPrompt(request, 2, 60) // cursor on deny
	if len(offsets) != len(permissionOptions()) {
		t.Fatalf("offsets = %d, want %d", len(offsets), len(permissionOptions()))
	}
	lines := strings.Split(plainRender(t, card), "\n")
	deny := offsets[2]
	if deny < 0 || deny >= len(lines) || !strings.Contains(lines[deny], "deny") {
		t.Fatalf("offset[2] (%d) should point at the deny line; lines=%#v", deny, lines)
	}
	if !strings.Contains(lines[deny], "▸") {
		t.Fatalf("the highlighted (cursor) option line should carry ▸, got %q", lines[deny])
	}
}

func TestPermissionRenderShowsNetworkTargetAndHostScopedAlways(t *testing.T) {
	request := agent.PermissionRequest{
		ToolName:   "web_fetch",
		SideEffect: "network",
		Scope:      "example.com",
	}
	card, _ := renderFocusedPermissionPrompt(request, 1, 72)
	got := plainRender(t, card)
	for _, want := range []string{"target: example.com", "always for this host", "[y]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("permission card = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "scope: example.com") {
		t.Fatalf("network prompt should render target label, got %q", got)
	}
}
