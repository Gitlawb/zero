package acp

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
)

func TestBuildPermissionOptions(t *testing.T) {
	req := agent.PermissionRequest{
		ToolCallID: "tc1",
		ToolName:   "bash",
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowForSession,
			agent.PermissionDecisionAllowPrefix,
			agent.PermissionDecisionAllowPrefixProject,
			agent.PermissionDecisionAlwaysAllow,
			agent.PermissionDecisionAlwaysAllowPrefix,
			agent.PermissionDecisionDeny,
			agent.PermissionDecisionCancel, // must be dropped (expressed as outcome)
		},
	}
	opts := buildPermissionOptions(req)
	// 7 options: allow, allow_for_session, allow_prefix, allow_prefix_for_project,
	// always_allow, always_allow_prefix, deny (cancel dropped). No breadth expansion
	// because CommandPrefixOptions is empty.
	if len(opts) != 7 {
		t.Fatalf("expected 7 options (cancel dropped), got %d: %+v", len(opts), opts)
	}
	// optionId must carry the ZERO action verbatim for a clean round trip.
	if opts[0].OptionID != string(agent.PermissionDecisionAllow) || opts[0].Kind != PermAllowOnce {
		t.Errorf("allow option = %+v", opts[0])
	}
	// The project-scoped prefix grant must be present (regression: it was silently
	// dropped because optionKindFor had no case for it).
	if opts[3].OptionID != string(agent.PermissionDecisionAllowPrefixProject) || opts[3].Kind != PermAllowAlways {
		t.Errorf("project prefix option = %+v", opts[3])
	}
	if opts[1].Kind != PermAllowAlways || opts[2].Kind != PermAllowAlways || opts[4].Kind != PermAllowAlways || opts[5].Kind != PermAllowAlways {
		t.Errorf("allow kinds = %q, %q, %q, %q", opts[1].Kind, opts[2].Kind, opts[4].Kind, opts[5].Kind)
	}
	if opts[6].OptionID != string(agent.PermissionDecisionDeny) || opts[6].Kind != PermRejectOnce {
		t.Errorf("deny option = %+v", opts[6])
	}
}

func TestBuildPermissionOptionsDefault(t *testing.T) {
	opts := buildPermissionOptions(agent.PermissionRequest{ToolName: "x"})
	if len(opts) != 2 {
		t.Fatalf("expected default allow+deny, got %d", len(opts))
	}
}

func TestDecisionFromOutcome(t *testing.T) {
	req := agent.PermissionRequest{
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAlwaysAllow,
			agent.PermissionDecisionDeny,
		},
	}
	if d := decisionFromOutcome(RequestPermissionOutcome{Outcome: OutcomeCancelled}, req); d.Action != agent.PermissionDecisionCancel {
		t.Errorf("cancelled -> %q, want cancel", d.Action)
	}
	if d := decisionFromOutcome(RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: "allow"}, req); d.Action != agent.PermissionDecisionAllow {
		t.Errorf("selected allow -> %q", d.Action)
	}
	if d := decisionFromOutcome(RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: "always_allow"}, req); d.Action != agent.PermissionDecisionAlwaysAllow {
		t.Errorf("selected always_allow -> %q", d.Action)
	}
	// Unknown option fails closed to deny.
	if d := decisionFromOutcome(RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: "bogus"}, req); d.Action != agent.PermissionDecisionDeny {
		t.Errorf("unknown option -> %q, want deny", d.Action)
	}
	// Missing/empty outcome fails closed to deny.
	if d := decisionFromOutcome(RequestPermissionOutcome{}, req); d.Action != agent.PermissionDecisionDeny {
		t.Errorf("empty outcome -> %q, want deny", d.Action)
	}
}

func TestBuildPermissionOptionsExpandsPrefixBreadths(t *testing.T) {
	req := agent.PermissionRequest{
		ToolName:      "bash",
		CommandPrefix: []string{"git", "push", "origin"},
		CommandPrefixOptions: [][]string{
			{"git", "push"},
			{"git", "push", "origin"},
		},
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowPrefix,
			agent.PermissionDecisionAllowPrefixProject,
			agent.PermissionDecisionAlwaysAllowPrefix,
			agent.PermissionDecisionDeny,
		},
	}
	opts := buildPermissionOptions(req)
	// allow + deny (1 each) + 3 prefix actions × 2 breadths = 8.
	if len(opts) != 8 {
		t.Fatalf("expected 8 options (2 breadths per prefix action), got %d: %+v", len(opts), opts)
	}
	// Each prefix breadth must round-trip its action AND its exact prefix.
	seen := map[string]bool{}
	for _, opt := range opts {
		action, prefix := decodeOptionID(opt.OptionID)
		if !isPrefixAction(action) {
			continue
		}
		if !prefixOffered(prefix, req.CommandPrefixOptions) {
			t.Errorf("expanded option %q decoded to a non-offered prefix %v", opt.Name, prefix)
		}
		seen[string(action)+"|"+strings.Join(prefix, " ")] = true
	}
	for _, action := range []agent.PermissionDecisionAction{
		agent.PermissionDecisionAllowPrefix,
		agent.PermissionDecisionAllowPrefixProject,
		agent.PermissionDecisionAlwaysAllowPrefix,
	} {
		for _, prefix := range req.CommandPrefixOptions {
			if !seen[string(action)+"|"+strings.Join(prefix, " ")] {
				t.Errorf("missing expanded option for %s / %v", action, prefix)
			}
		}
	}
}

func TestDecisionFromOutcomePrefixBreadthRoundTrip(t *testing.T) {
	req := agent.PermissionRequest{
		CommandPrefix: []string{"git", "push", "origin"},
		CommandPrefixOptions: [][]string{
			{"git", "push"},
			{"git", "push", "origin"},
		},
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllowPrefixProject,
			agent.PermissionDecisionDeny,
		},
	}

	// Selecting the broader `git push` breadth for the project scope must carry
	// both the action and the chosen prefix back to ZERO.
	id := encodeOptionID(agent.PermissionDecisionAllowPrefixProject, []string{"git", "push"})
	d := decisionFromOutcome(RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: id}, req)
	if d.Action != agent.PermissionDecisionAllowPrefixProject {
		t.Fatalf("action = %q, want allow_prefix_for_project", d.Action)
	}
	if strings.Join(d.CommandPrefix, " ") != "git push" {
		t.Fatalf("prefix = %v, want [git push]", d.CommandPrefix)
	}

	// A prefix that was never offered fails closed to deny (no silent widening).
	tampered := encodeOptionID(agent.PermissionDecisionAllowPrefixProject, []string{"git"})
	if d := decisionFromOutcome(RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: tampered}, req); d.Action != agent.PermissionDecisionDeny {
		t.Fatalf("non-offered prefix -> %q, want deny", d.Action)
	}
}

func TestPermissionToolCall(t *testing.T) {
	tc := permissionToolCall(agent.PermissionRequest{
		ToolCallID: "tc9",
		ToolName:   "read_file",
		Args:       map[string]any{"path": "a.go"},
	})
	if tc.ToolCallID != "tc9" || tc.Kind != ToolKindRead || tc.Status != ToolStatusPending {
		t.Fatalf("unexpected toolCall: %+v", tc)
	}
	if tc.Title != "read_file a.go" {
		t.Errorf("title = %q", tc.Title)
	}
	if len(tc.RawInput) == 0 {
		t.Error("expected rawInput from args")
	}
}
