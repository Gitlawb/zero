package execution

import "testing"

func TestRequestValidationRequiresConcreteExecutionContext(t *testing.T) {
	request := Request{
		Origin:           OriginInteractiveCommand,
		Mode:             ModeCaptured,
		Command:          Command{Name: "/bin/sh", Args: []string{"-c", "true"}},
		WorkingDirectory: "/workspace",
		WorkspaceRoots:   []string{"/workspace"},
		Capabilities: []Capability{{
			Kind:  CapabilityWorkspaceWrite,
			Scope: "/workspace",
		}},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	request.Origin = ""
	if err := request.Validate(); err == nil {
		t.Fatal("request without origin unexpectedly validated")
	}
}

func TestOutcomeValidationPinsTerminalStateContracts(t *testing.T) {
	tests := []struct {
		name    string
		outcome Outcome
		valid   bool
	}{
		{
			name:    "retained process",
			outcome: Outcome{State: StateRetained, Kind: OutcomeRunning, ProcessID: "1000"},
			valid:   true,
		},
		{
			name:    "successful completion",
			outcome: Outcome{State: StateCompleted, Kind: OutcomeSuccess, Exit: &Exit{Code: 0}},
			valid:   true,
		},
		{
			name: "structured denial",
			outcome: Outcome{
				State: StateDenied,
				Kind:  OutcomePolicyDenied,
				Denial: &Denial{
					Capability: Capability{Kind: CapabilityProtectedMetadata, Scope: "/workspace/.zero"},
					Source:     DenialSourceConfiguredPolicy,
					Reason:     "protected workspace metadata cannot be created",
					NextAction: DenialNextActionRequestApproval,
				},
			},
			valid: true,
		},
		{
			name:    "retained without process id",
			outcome: Outcome{State: StateRetained, Kind: OutcomeRunning},
		},
		{
			name:    "denied without denial",
			outcome: Outcome{State: StateDenied, Kind: OutcomePolicyDenied},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.outcome.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid outcome unexpectedly validated")
			}
		})
	}
}
