package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

func pendingPermissionModel(t *testing.T, decide func(agent.PermissionDecision)) model {
	t.Helper()
	return pendingPermissionModelWithRequest(t, testPromptPermissionRequest(), decide)
}

func pendingPermissionModelWithRequest(t *testing.T, request agent.PermissionRequest, decide func(agent.PermissionDecision)) model {
	t.Helper()
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	updated, _ := m.Update(permissionRequestMsg{
		runID:   7,
		request: request,
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
		t.Fatalf("default cursor = %d, want 0 (approve)", m.pendingPermission.cursor)
	}
}

func TestPermissionOptionsEmptyDecisionsUseRecoverableFallback(t *testing.T) {
	options := permissionOptions(agent.PermissionRequest{})
	if len(options) != 2 {
		t.Fatalf("fallback options = %#v, want allow and deny only", options)
	}
	if options[0].choice != permissionDecisionAllow || options[1].choice != permissionDecisionDeny {
		t.Fatalf("fallback options = %#v, want allow then deny", options)
	}
}

func TestPermissionOptionsExposeApprovalCancelWhenSupplied(t *testing.T) {
	request := agent.PermissionRequest{
		ToolName: "bash",
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowForSession,
			agent.PermissionDecisionDeny,
			agent.PermissionDecisionCancel,
		},
	}
	options := permissionOptions(request)
	if len(options) != 4 {
		t.Fatalf("options = %#v, want four supplied choices", options)
	}
	if options[2].choice != permissionDecisionDeny || options[2].hotkey != "d" {
		t.Fatalf("recoverable deny option = %#v, want deny on d", options[2])
	}
	if options[3].choice != permissionDecisionCancel || options[3].hotkey != "n" {
		t.Fatalf("cancel option = %#v, want cancel on n", options[3])
	}

	card, _ := renderFocusedPermissionPrompt(request, 3, 80)
	got := plainRender(t, card)
	for _, want := range []string{"continue without running it", "[d]", "tell Zero what to do differently", "[n]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("permission card = %q, missing %q", got, want)
		}
	}
}

func TestPermissionOptionsExposeCommandPrefixApproval(t *testing.T) {
	request := agent.PermissionRequest{
		ToolName:      "bash",
		CommandPrefix: []string{"git", "status"},
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowPrefix,
			agent.PermissionDecisionCancel,
		},
	}
	options := permissionOptions(request)
	if len(options) != 3 || options[1].choice != permissionDecisionAllowPrefix || options[1].hotkey != "p" {
		t.Fatalf("prefix option = %#v, want p hotkey in supplied order", options)
	}
	card, _ := renderFocusedPermissionPrompt(request, 1, 100)
	got := plainRender(t, card)
	for _, want := range []string{"allow `git status` in this session", "[p]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("permission card = %q, missing %q", got, want)
		}
	}
}

func TestPermissionOptionsCanExposePatchCancelWithoutRecoverableDeny(t *testing.T) {
	request := agent.PermissionRequest{
		ToolName: "apply_patch",
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowForSession,
			agent.PermissionDecisionCancel,
		},
	}
	card, _ := renderFocusedPermissionPrompt(request, 2, 80)
	got := plainRender(t, card)
	if !strings.Contains(got, "tell Zero what to do differently") || !strings.Contains(got, "[n]") {
		t.Fatalf("permission card = %q, missing cancel option", got)
	}
	if strings.Contains(got, "continue without running it") || strings.Contains(got, "[d]") {
		t.Fatalf("apply_patch approval must not show recoverable deny, got %q", got)
	}
}

func TestRequestPermissionsPromptUsesGrantLabelsAndEscDenies(t *testing.T) {
	request := agent.PermissionRequest{
		ToolName: tools.RequestPermissionsToolName,
		Action:   agent.PermissionActionPrompt,
		Reason:   "Need write access.",
		Scope:    "write /tmp/project",
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowStrict,
			agent.PermissionDecisionAllowForSession,
			agent.PermissionDecisionDeny,
		},
	}
	card, _ := renderFocusedPermissionPrompt(request, 1, 96)
	got := plainRender(t, card)
	for _, want := range []string{
		"Grant requested permissions?",
		"permissions: write /tmp/project",
		"Grant for this task",
		"Grant for this task and ask model to review commands",
		"Grant for this session",
		"Continue without permissions",
		"[esc] continue without permissions",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("request_permissions card = %q, missing %q", got, want)
		}
	}

	var resolved agent.PermissionDecision
	m := pendingPermissionModelWithRequest(t, request, func(d agent.PermissionDecision) {
		resolved = d
	})
	updated, _ := m.Update(testKey(tea.KeyEsc))
	m = updated.(model)
	if resolved.Action != agent.PermissionDecisionDeny {
		t.Fatalf("Esc should continue without permissions, got %#v", resolved)
	}
	if m.pendingPermission != nil {
		t.Fatal("request_permissions prompt should clear after Esc")
	}
}

func TestPermissionCursorMovesAndEnterConfirms(t *testing.T) {
	decisions := []permissionDecision{}
	m := pendingPermissionModel(t, func(d agent.PermissionDecision) {
		decisions = append(decisions, permissionDecision(d.Action))
	})
	// 0 -> down 1 -> down 2 -> up 1 (session).
	for _, key := range []rune{tea.KeyDown, tea.KeyDown, tea.KeyUp} {
		updated, _ := m.Update(testKey(key))
		m = updated.(model)
	}
	if m.pendingPermission == nil || m.pendingPermission.cursor != 1 {
		t.Fatalf("cursor after down,down,up = %v, want 1 (session)", m.pendingPermission)
	}
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if len(decisions) != 1 || decisions[0] != permissionDecisionAllowForSession {
		t.Fatalf("enter on cursor 1 should resolve session allow, got %#v", decisions)
	}
	if m.pendingPermission != nil {
		t.Fatal("prompt should clear after confirm")
	}
}

func TestPermissionCursorWrapsWithUp(t *testing.T) {
	m := pendingPermissionModel(t, func(agent.PermissionDecision) {})
	updated, _ := m.Update(testKey(tea.KeyUp)) // 0 wraps to last (deny)
	m = updated.(model)
	if want := len(permissionOptions(m.pendingPermission.request)) - 1; m.pendingPermission.cursor != want {
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

func TestPermissionCancelHotkeyResolvesDirectly(t *testing.T) {
	request := testPromptPermissionRequest()
	request.ToolName = "bash"
	request.AvailableDecisions = []agent.PermissionDecisionAction{
		agent.PermissionDecisionAllow,
		agent.PermissionDecisionDeny,
		agent.PermissionDecisionCancel,
	}
	got := []permissionDecision{}
	m := pendingPermissionModelWithRequest(t, request, func(d agent.PermissionDecision) {
		got = append(got, permissionDecision(d.Action))
	})
	if _, cmd := m.Update(testKeyText("n")); cmd != nil {
		t.Fatal("'n' should resolve synchronously")
	}
	if len(got) != 1 || got[0] != permissionDecisionCancel {
		t.Fatalf("'n' should resolve cancel directly, got %#v", got)
	}
}

func TestPermissionRenderEmitsHighlightedClickableOffsets(t *testing.T) {
	request := agent.PermissionRequest{ToolName: "write_file", AvailableDecisions: testAllPermissionDecisions()}
	card, offsets := renderFocusedPermissionPrompt(request, 2, 60) // cursor on future approval
	if len(offsets) != len(permissionOptions(request)) {
		t.Fatalf("offsets = %d, want %d", len(offsets), len(permissionOptions(request)))
	}
	lines := strings.Split(plainRender(t, card), "\n")
	future := offsets[2]
	if future < 0 || future >= len(lines) || !strings.Contains(lines[future], "always") {
		t.Fatalf("offset[2] (%d) should point at the future line; lines=%#v", future, lines)
	}
	if !strings.Contains(lines[future], "▸") {
		t.Fatalf("the highlighted (cursor) option line should carry ▸, got %q", lines[future])
	}
}

func TestPermissionRenderShowsNetworkTargetAndHostScopedAlways(t *testing.T) {
	request := agent.PermissionRequest{
		ToolName:           "web_fetch",
		SideEffect:         "network",
		Scope:              "example.com",
		AvailableDecisions: testAllPermissionDecisions(),
	}
	card, _ := renderFocusedPermissionPrompt(request, 1, 72)
	got := plainRender(t, card)
	for _, want := range []string{"target: example.com", "allow this host for this conversation", "[s]", "allow this host in the future", "[y]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("permission card = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "scope: example.com") {
		t.Fatalf("network prompt should render target label, got %q", got)
	}
}
