package acp

import (
	"encoding/json"
	"slices"

	"github.com/Gitlawb/zero/internal/agent"
)

// permission.go maps ZERO's permission prompt model onto ACP's
// session/request_permission request/option/outcome model. The option id on the
// wire carries the ZERO decision action verbatim, so mapping the client's
// selection back to a ZERO decision is exact and lossless.

// buildPermissionOptions turns the decisions ZERO offers for a tool call into ACP
// PermissionOptions. Only the actions ZERO actually presented (AvailableDecisions)
// are surfaced; the optionId is the ZERO action string for a clean round-trip.
func buildPermissionOptions(req agent.PermissionRequest) []PermissionOption {
	actions := req.AvailableDecisions
	if len(actions) == 0 {
		// Sensible default if ZERO didn't enumerate: allow once / reject.
		actions = []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionDeny,
		}
	}
	options := make([]PermissionOption, 0, len(actions))
	for _, action := range actions {
		kind, name := optionKindFor(action)
		if kind == "" {
			continue // skip actions that have no clean ACP option (e.g. cancel)
		}
		options = append(options, PermissionOption{
			OptionID: string(action),
			Name:     name,
			Kind:     kind,
		})
	}
	return options
}

// optionKindFor maps a ZERO decision action to an ACP PermissionOptionKind and a
// human label. Returns an empty kind for actions that ACP expresses through the
// outcome rather than an option (cancel).
func optionKindFor(action agent.PermissionDecisionAction) (kind, name string) {
	switch action {
	case agent.PermissionDecisionAllow, agent.PermissionDecisionAllowStrict:
		return PermAllowOnce, "Allow"
	case agent.PermissionDecisionAllowForSession:
		return PermAllowAlways, "Allow for this session"
	case agent.PermissionDecisionAllowPrefix:
		return PermAllowAlways, "Allow this command for the session"
	case agent.PermissionDecisionAlwaysAllow:
		return PermAllowAlways, "Always allow"
	case agent.PermissionDecisionAlwaysAllowPrefix:
		return PermAllowAlways, "Always allow this command"
	case agent.PermissionDecisionDeny:
		return PermRejectOnce, "Reject"
	case agent.PermissionDecisionCancel:
		return "", "" // expressed as outcome=cancelled, not an option
	default:
		return "", ""
	}
}

// decisionFromOutcome maps the client's permission outcome back to a ZERO
// decision. A cancelled outcome cancels the run; a selected option id is the ZERO
// action verbatim (validated against what was offered); anything unrecognized
// fails closed to deny.
func decisionFromOutcome(outcome RequestPermissionOutcome, offered []agent.PermissionDecisionAction) agent.PermissionDecision {
	switch outcome.Outcome {
	case OutcomeCancelled:
		return agent.PermissionDecision{Action: agent.PermissionDecisionCancel, Reason: "client cancelled"}
	case OutcomeSelected:
		action := agent.PermissionDecisionAction(outcome.OptionID)
		// Bind to what was actually offered for THIS call: a client must not be able
		// to return a broader grant (always_allow / allow_for_session) that wasn't
		// presented. Anything not offered fails closed to deny.
		if actionOffered(action, offered) {
			return agent.PermissionDecision{Action: action}
		}
		return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "permission option was not offered"}
	default:
		return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "no permission outcome"}
	}
}

func actionOffered(action agent.PermissionDecisionAction, offered []agent.PermissionDecisionAction) bool {
	return slices.Contains(offered, action)
}

// permissionToolCall builds the ToolCall descriptor embedded in a
// session/request_permission request from a ZERO permission request.
func permissionToolCall(req agent.PermissionRequest) ToolCallUpdate {
	args := marshalArgs(req.Args)
	return ToolCallUpdate{
		ToolCallID: req.ToolCallID,
		Title:      toolTitle(req.ToolName, string(args)),
		Kind:       toolKindFor(req.ToolName),
		Status:     ToolStatusPending,
		RawInput:   rawInputBytes(args),
	}
}

func marshalArgs(args map[string]any) []byte {
	if len(args) == 0 {
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return data
}

func rawInputBytes(data []byte) json.RawMessage {
	if len(data) == 0 || !json.Valid(data) {
		return nil
	}
	return json.RawMessage(data)
}
