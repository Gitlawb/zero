package tui

import (
	"strings"

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
	// commandPrefix, when set, is the exact breadth this option grants — used
	// when a prefix decision is expanded into one option per breadth choice.
	// Empty means the loop grants the request's default CommandPrefix.
	commandPrefix []string
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
			options = appendPrefixOptions(options, request, permissionDecisionAllowPrefix, "p", "allow command prefix for session", "prefix (session)")
		case agent.PermissionDecisionAllowPrefixProject:
			options = appendPrefixOptions(options, request, permissionDecisionAllowPrefixProject, "j", "allow command prefix for this project", "prefix (project)")
		case agent.PermissionDecisionAlwaysAllowPrefix:
			options = appendPrefixOptions(options, request, permissionDecisionAlwaysAllowPrefix, "y", "allow command prefix globally", "prefix (global)")
		case agent.PermissionDecisionAlwaysAllow:
			options = append(options, permissionOption{label: "allow in future", hotkey: "f", choice: permissionDecisionAlwaysAllow})
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

// appendPrefixOptions adds prefix-grant choices. With a single safe prefix it
// keeps one option carrying the request's default label and hotkey. When the
// request offers several breadths it expands into one option per breadth (e.g.
// "prefix (session): npm run *"), so the approver can pick how wide the grant is;
// the breadth equal to the request's default prefix keeps the hotkey.
func appendPrefixOptions(options []permissionOption, request agent.PermissionRequest, choice permissionDecision, hotkey, singleLabel, verb string) []permissionOption {
	ladder := request.CommandPrefixOptions
	if len(ladder) <= 1 {
		return append(options, permissionOption{label: singleLabel, hotkey: hotkey, choice: choice})
	}
	for _, prefix := range ladder {
		option := permissionOption{
			label:         verb + ": " + strings.Join(prefix, " "),
			choice:        choice,
			commandPrefix: append([]string(nil), prefix...),
		}
		if equalStringPrefix(prefix, request.CommandPrefix) {
			option.hotkey = hotkey
		}
		options = append(options, option)
	}
	return options
}

func equalStringPrefix(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
	if m.pendingPermission == nil || m.pendingPermission.typing {
		// While typing feedback the arrow/Tab keys belong to the text field, not
		// the option list.
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
// Enter-key counterpart to the a/y/d hotkeys and a mouse click. Confirming the
// "tell Zero what to do differently" choice opens the inline feedback field
// instead of resolving immediately.
func (m model) confirmPermissionCursor() (tea.Model, tea.Cmd) {
	if m.pendingPermission == nil {
		return m, nil
	}
	if m.pendingPermission.typing {
		return m.submitPermissionFeedback()
	}
	option := permissionOptions(m.pendingPermission.request)[clampPermissionCursor(m.pendingPermission.cursor, m.pendingPermission.request)]
	return m.choosePermissionOption(option)
}

// choosePermissionOption applies a chosen option. The cancel choice (the "tell
// Zero what to do differently" row and its [n] hotkey) opens the inline feedback
// field rather than aborting the run; every other choice resolves immediately,
// carrying the option's command-prefix breadth through resolvePermissionOption.
func (m model) choosePermissionOption(option permissionOption) (tea.Model, tea.Cmd) {
	if m.pendingPermission == nil {
		return m, nil
	}
	if option.choice == permissionDecisionCancel {
		m.pendingPermission.typing = true
		// Preserve whatever the user had drafted/queued in the composer so it is
		// restored when they leave feedback mode (submit or cancel).
		m.pendingPermission.savedDraft = m.input.Value()
		m.input.SetValue("")
		return m, nil
	}
	return m.resolvePermissionOption(option)
}

// submitPermissionFeedback ends the feedback field. Non-empty text is sent as a
// Deny decision whose Reason is the text: the agent surfaces that as the tool
// result (deniedPermissionResult) so the model reads the instruction and adjusts
// in the same turn, rather than the run being cancelled. Empty text falls back to
// a plain cancel, matching the option's prior behaviour.
func (m model) submitPermissionFeedback() (tea.Model, tea.Cmd) {
	if m.pendingPermission == nil {
		return m, nil
	}
	feedback := strings.TrimSpace(m.input.Value())
	// Restore the composer draft the user had before entering feedback mode; the
	// feedback text itself is delivered via the decision Reason, not the composer.
	m.input.SetValue(m.pendingPermission.savedDraft)
	m.pendingPermission.typing = false
	if feedback == "" {
		return m.resolvePermission(permissionDecisionCancel)
	}
	return m.resolvePermissionWithReason(permissionDecisionDeny, feedback)
}

// cancelPermissionTyping returns from the feedback field to the option list
// without resolving, so Esc is a safe "I didn't mean to type" back-out.
func (m model) cancelPermissionTyping() (tea.Model, tea.Cmd) {
	if m.pendingPermission == nil || !m.pendingPermission.typing {
		return m, nil
	}
	m.pendingPermission.typing = false
	m.input.SetValue(m.pendingPermission.savedDraft)
	m.pendingPermission.savedDraft = ""
	return m, nil
}
