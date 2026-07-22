package acp

import (
	"encoding/json"
	"strings"

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
		// A command-prefix grant can be offered at several breadths (e.g. `git`,
		// `git push`, `git push origin`). Expand each into its own ACP option so the
		// client can pick how wide the grant is; the chosen breadth is encoded into
		// the option id and validated on the way back. Without this, ACP clients can
		// only ever grant the exact default prefix.
		if isPrefixAction(action) && len(req.CommandPrefixOptions) > 1 {
			for _, prefix := range req.CommandPrefixOptions {
				options = append(options, PermissionOption{
					OptionID: encodeOptionID(action, prefix),
					Name:     name + ": " + strings.Join(prefix, " "),
					Kind:     kind,
				})
			}
			continue
		}
		options = append(options, PermissionOption{
			OptionID: string(action),
			Name:     name,
			Kind:     kind,
		})
	}
	return options
}

// isPrefixAction reports whether an action grants a command prefix, which is the
// only decision family that carries a breadth choice (CommandPrefixOptions).
func isPrefixAction(action agent.PermissionDecisionAction) bool {
	switch action {
	case agent.PermissionDecisionAllowPrefix,
		agent.PermissionDecisionAllowPrefixProject,
		agent.PermissionDecisionAlwaysAllowPrefix:
		return true
	default:
		return false
	}
}

// encodeOptionID packs the ZERO action plus the chosen command-prefix breadth into
// the ACP option id so the selection round-trips exactly. An action with no
// breadth keeps the bare action string, so non-prefix options stay backward
// compatible on the wire.
func encodeOptionID(action agent.PermissionDecisionAction, prefix []string) string {
	if len(prefix) == 0 {
		return string(action)
	}
	payload, err := json.Marshal(optionIDPayload{Action: string(action), Prefix: prefix})
	if err != nil {
		return string(action)
	}
	return string(payload)
}

// decodeOptionID reverses encodeOptionID. A JSON payload yields the action and its
// breadth; anything else is treated as a bare action string (no breadth).
func decodeOptionID(id string) (agent.PermissionDecisionAction, []string) {
	trimmed := strings.TrimSpace(id)
	if strings.HasPrefix(trimmed, "{") {
		var payload optionIDPayload
		if err := json.Unmarshal([]byte(trimmed), &payload); err == nil && payload.Action != "" {
			return agent.PermissionDecisionAction(payload.Action), payload.Prefix
		}
	}
	return agent.PermissionDecisionAction(id), nil
}

type optionIDPayload struct {
	Action string   `json:"action"`
	Prefix []string `json:"prefix"`
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
	case agent.PermissionDecisionAllowPrefixProject:
		return PermAllowAlways, "Allow this command for this project"
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
func decisionFromOutcome(outcome RequestPermissionOutcome, req agent.PermissionRequest) agent.PermissionDecision {
	switch outcome.Outcome {
	case OutcomeCancelled:
		return agent.PermissionDecision{Action: agent.PermissionDecisionCancel, Reason: "client cancelled"}
	case OutcomeSelected:
		action, prefix := decodeOptionID(outcome.OptionID)
		// Bind to what was actually offered for THIS call: a client must not be able
		// to return a broader grant (always_allow / allow_for_session) that wasn't
		// presented. Anything not offered fails closed to deny.
		if !actionOffered(action, req.AvailableDecisions) {
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "permission option was not offered"}
		}
		decision := agent.PermissionDecision{Action: action}
		if len(prefix) > 0 {
			// The breadth must be one of the rungs this call offered, so a client can
			// only widen the grant to a presented breadth — never an arbitrary prefix.
			if !prefixOffered(prefix, req.CommandPrefixOptions) {
				return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "command prefix was not offered"}
			}
			decision.CommandPrefix = append([]string(nil), prefix...)
		}
		return decision
	default:
		return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "no permission outcome"}
	}
}

func actionOffered(action agent.PermissionDecisionAction, offered []agent.PermissionDecisionAction) bool {
	for _, a := range offered {
		if a == action {
			return true
		}
	}
	return false
}

// prefixOffered reports whether prefix exactly matches one of the offered breadth
// rungs, so a selected grant can only be one the request actually presented.
func prefixOffered(prefix []string, offered [][]string) bool {
	for _, candidate := range offered {
		if len(candidate) != len(prefix) {
			continue
		}
		match := true
		for i := range candidate {
			if candidate[i] != prefix[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
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
