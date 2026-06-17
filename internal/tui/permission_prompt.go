package tui

import (
	tea "charm.land/bubbletea/v2"
)

// permissionOption is one selectable choice in the permission popup. The slice
// order is both the on-screen order and the cursor index space; index 0 is the
// resting default highlight.
type permissionOption struct {
	label  string
	hotkey string
	choice permissionDecision
}

// permissionOptions returns the ordered choices the popup offers. "Allow once"
// is first so it is the resting highlight (the common response a user attends
// to a prompt to give); "deny" is last. The hotkeys mirror handlePermissionKey.
func permissionOptions() []permissionOption {
	return []permissionOption{
		{label: "allow once", hotkey: "a", choice: permissionDecisionAllow},
		{label: "always", hotkey: "y", choice: permissionDecisionAlwaysAllow},
		{label: "deny", hotkey: "d", choice: permissionDecisionDeny},
	}
}

// clampPermissionCursor keeps a cursor index within the option range.
func clampPermissionCursor(cursor int) int {
	n := len(permissionOptions())
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
	n := len(permissionOptions())
	cursor := (clampPermissionCursor(m.pendingPermission.cursor) + delta) % n
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
	option := permissionOptions()[clampPermissionCursor(m.pendingPermission.cursor)]
	return m.resolvePermission(option.choice)
}
