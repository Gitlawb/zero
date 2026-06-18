package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
)

// permissionOption is one selectable choice in the permission popup. The slice
// order is both the on-screen order and the cursor index space; index 0 is the
// resting default highlight.
type permissionOption struct {
	label  string
	hotkey string
	choice permissionDecision
}

// permissionOptions returns the ordered choices the popup offers. The backend
// supplies the decision set because network, file, and generic command prompts
// can validly expose different scopes.
func permissionOptions(request agent.PermissionRequest) []permissionOption {
	decisions := request.AvailableDecisions
	if len(decisions) == 0 {
		decisions = []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionDeny,
		}
	}
	options := make([]permissionOption, 0, len(decisions))
	seen := map[agent.PermissionDecisionAction]bool{}
	for _, decision := range decisions {
		if seen[decision] {
			continue
		}
		seen[decision] = true
		switch decision {
		case agent.PermissionDecisionAllow:
			options = append(options, permissionOption{label: "allow once", hotkey: "a", choice: permissionDecisionAllow})
		case agent.PermissionDecisionAllowStrict:
			options = append(options, permissionOption{label: "allow with review", hotkey: "r", choice: permissionDecisionAllowStrict})
		case agent.PermissionDecisionAllowForSession:
			options = append(options, permissionOption{label: "allow for session", hotkey: "s", choice: permissionDecisionAllowForSession})
		case agent.PermissionDecisionAllowPrefix:
			options = append(options, permissionOption{label: "allow command prefix for session", hotkey: "p", choice: permissionDecisionAllowPrefix})
		case agent.PermissionDecisionAlwaysAllowPrefix:
			options = append(options, permissionOption{label: "always allow command prefix", hotkey: "y", choice: permissionDecisionAlwaysAllowPrefix})
		case agent.PermissionDecisionAlwaysAllow:
			options = append(options, permissionOption{label: "always", hotkey: "y", choice: permissionDecisionAlwaysAllow})
		case agent.PermissionDecisionDeny:
			options = append(options, permissionOption{label: "deny", hotkey: "d", choice: permissionDecisionDeny})
		case agent.PermissionDecisionCancel:
			options = append(options, permissionOption{label: "cancel", hotkey: "n", choice: permissionDecisionCancel})
		}
	}
	if len(options) == 0 {
		return []permissionOption{{label: "deny", hotkey: "d", choice: permissionDecisionDeny}}
	}
	return options
}

// clampPermissionCursor keeps a cursor index within the option range.
func clampPermissionCursor(cursor int, request agent.PermissionRequest) int {
	n := len(permissionOptions(request))
	if cursor < 0 {
		return 0
	}
	if cursor >= n {
		return n - 1
	}
	return cursor
}

// movePermissionCursor advances the highlighted option by delta, wrapping around
// the ends. A no-op when no permission prompt is pending. The cursor lives on the
// pending prompt (a pointer), mirroring how the picker's selection moves.
func (m model) movePermissionCursor(delta int) model {
	if m.pendingPermission == nil {
		return m
	}
	n := len(permissionOptions(m.pendingPermission.request))
	cursor := (clampPermissionCursor(m.pendingPermission.cursor, m.pendingPermission.request) + delta) % n
	if cursor < 0 {
		cursor += n
	}
	m.pendingPermission.cursor = cursor
	return m
}

// confirmPermissionCursor resolves the currently highlighted option. It is the
// Enter-key counterpart to the a/y/d hotkeys and a mouse click.
func (m model) confirmPermissionCursor() (tea.Model, tea.Cmd) {
	if m.pendingPermission == nil {
		return m, nil
	}
	option := permissionOptions(m.pendingPermission.request)[clampPermissionCursor(m.pendingPermission.cursor, m.pendingPermission.request)]
	return m.resolvePermission(option.choice)
}
