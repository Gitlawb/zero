package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/trace"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestProfileControllerNilPolicyIsNoOp(t *testing.T) {
	for _, controller := range []*profileController{
		newProfileController(nil),
		newProfileController(&ProfilePolicy{Name: "balanced"}), // Escalate nil
	} {
		controller.observeToolOutcome(toolFailureOutcome{Count: 99}, ToolResult{Status: tools.StatusOK, Risk: sandbox.Risk{Level: sandbox.RiskCritical}})
		controller.observeUncertain()
		controller.observeSelfCorrect(OutcomeAborted)
		if _, fired := controller.maybeEscalate(); fired {
			t.Fatal("nil policy must never escalate")
		}
	}
}

func TestProfileControllerEscalatesOnFailureStreak(t *testing.T) {
	controller := newProfileController(&ProfilePolicy{Escalate: &PostureEscalation{OnToolFailureStreak: 2, MaxTurns: 80}})
	controller.observeToolOutcome(toolFailureOutcome{Count: 1}, ToolResult{Status: tools.StatusError})
	if _, fired := controller.maybeEscalate(); fired {
		t.Fatal("streak below threshold must not escalate")
	}
	controller.observeToolOutcome(toolFailureOutcome{Count: 2}, ToolResult{Status: tools.StatusError})
	target, fired := controller.maybeEscalate()
	if !fired || target.MaxTurns != 80 {
		t.Fatalf("expected escalation with the policy target, got fired=%t target=%+v", fired, target)
	}
}

func TestProfileControllerEscalatesOnRiskyMutation(t *testing.T) {
	controller := newProfileController(&ProfilePolicy{Escalate: &PostureEscalation{OnRiskyMutation: sandbox.RiskHigh}})
	// Medium risk on an executed result: below threshold.
	controller.observeToolOutcome(toolFailureOutcome{}, ToolResult{Status: tools.StatusOK, Risk: sandbox.Risk{Level: sandbox.RiskMedium}})
	if _, fired := controller.maybeEscalate(); fired {
		t.Fatal("medium risk must not trip a high threshold")
	}
	// Critical risk on a DENIED result: never counts.
	controller.observeToolOutcome(toolFailureOutcome{}, ToolResult{Status: tools.StatusError, DenialReason: DenialSandboxBlock, Risk: sandbox.Risk{Level: sandbox.RiskCritical}})
	if _, fired := controller.maybeEscalate(); fired {
		t.Fatal("a denied result must not trip the risky-mutation signal")
	}
	controller.observeToolOutcome(toolFailureOutcome{}, ToolResult{Status: tools.StatusOK, Risk: sandbox.Risk{Level: sandbox.RiskCritical}})
	if _, fired := controller.maybeEscalate(); !fired {
		t.Fatal("critical executed mutation must trip a high threshold")
	}
}

func TestProfileControllerEscalatesOnSelfCorrectFailure(t *testing.T) {
	controller := newProfileController(&ProfilePolicy{Escalate: &PostureEscalation{OnSelfCorrectFailure: true}})
	controller.observeSelfCorrect(OutcomePassed)
	controller.observeSelfCorrect(OutcomeDisabled)
	if _, fired := controller.maybeEscalate(); fired {
		t.Fatal("passing/disabled outcomes must not escalate")
	}
	controller.observeSelfCorrect(OutcomeReported)
	if _, fired := controller.maybeEscalate(); !fired {
		t.Fatal("a failing verification outcome must escalate")
	}
}

func TestProfileControllerEscalatesOnUncertainCompletion(t *testing.T) {
	controller := newProfileController(&ProfilePolicy{Escalate: &PostureEscalation{OnCompletionUncertain: 2}})
	controller.observeUncertain()
	if _, fired := controller.maybeEscalate(); fired {
		t.Fatal("first uncertain evaluation must not trip a threshold of 2")
	}
	controller.observeUncertain()
	if _, fired := controller.maybeEscalate(); !fired {
		t.Fatal("second uncertain evaluation must escalate")
	}
}

func TestProfileControllerEscalatesAtMostOnce(t *testing.T) {
	controller := newProfileController(&ProfilePolicy{Escalate: &PostureEscalation{OnToolFailureStreak: 1}})
	controller.observeToolOutcome(toolFailureOutcome{Count: 1}, ToolResult{Status: tools.StatusError})
	if _, fired := controller.maybeEscalate(); !fired {
		t.Fatal("expected first escalation")
	}
	controller.observeToolOutcome(toolFailureOutcome{Count: 5}, ToolResult{Status: tools.StatusOK, Risk: sandbox.Risk{Level: sandbox.RiskCritical}})
	controller.observeUncertain()
	if _, fired := controller.maybeEscalate(); fired {
		t.Fatal("escalation must be one-shot per run")
	}
}

func TestOptionalEventKeysIncludePostureEscalations(t *testing.T) {
	for _, key := range trace.OptionalEventKeys() {
		if key == "counter:"+trace.CounterPostureEscalations {
			return
		}
	}
	t.Fatalf("posture_escalations missing from OptionalEventKeys: %v", trace.OptionalEventKeys())
}

// failingProfileTool always errors with the same signature so the repeated-
// failure guard builds a streak.
type failingProfileTool struct{}

func (failingProfileTool) Name() string        { return "flaky_probe" }
func (failingProfileTool) Description() string { return "always fails identically (test)" }
func (failingProfileTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}
func (failingProfileTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}
}
func (failingProfileTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusError, Output: "probe exploded: same signature"}
}

// TestPostureEscalationRaisesTurnCeilingMidRun proves the end-to-end act path:
// a Fast-style run with a 2-turn ceiling hits a failure streak, escalates, and
// finishes on a turn the original ceiling would have cut off, with the effort
// target applied to the post-escalation request and the counter emitted.
func TestPostureEscalationRaisesTurnCeilingMidRun(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(failingProfileTool{})

	toolTurn := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "flaky_probe"},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"},
		{Type: zeroruntime.StreamEventDone},
	}
	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{
		toolTurn,
		toolTurn,
		{
			{Type: zeroruntime.StreamEventText, Content: "recovered after escalation"},
			{Type: zeroruntime.StreamEventDone},
		},
	}}

	recorder := trace.NewRecorder("posture-session", "run-1", "fast")
	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: 2,
		Trace:    recorder,
		Profile: &ProfilePolicy{
			Name: "fast",
			Escalate: &PostureEscalation{
				MaxTurns:            4,
				ReasoningEffort:     "high",
				OnToolFailureStreak: 2,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "recovered after escalation" {
		t.Fatalf("expected the run to continue past the original 2-turn ceiling, got %q (turns=%d)", result.FinalAnswer, result.Turns)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected 3 requests (ceiling raised from 2 to 4), got %d", len(provider.requests))
	}
	if provider.requests[2].ReasoningEffort != "high" {
		t.Fatalf("expected the post-escalation request to carry the target effort, got %q", provider.requests[2].ReasoningEffort)
	}
	tr := recorder.Finish()
	if got := tr.Counter(trace.CounterPostureEscalations); got != 1 {
		t.Fatalf("posture_escalations = %d, want 1", got)
	}
}

// TestPostureEscalationAbsentWithoutProfile pins the no-regression invariant at
// the loop level: the identical failing script without a profile ends at the
// original ceiling with the max-turns answer.
func TestPostureEscalationAbsentWithoutProfile(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(failingProfileTool{})

	toolTurn := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "flaky_probe"},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"},
		{Type: zeroruntime.StreamEventDone},
	}
	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{toolTurn, toolTurn}}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) > 3 {
		t.Fatalf("unprofiled run must not extend the ceiling, got %d requests", len(provider.requests))
	}
	if !strings.Contains(result.FinalAnswer, "maximum number of turns") && result.FinalAnswer == "recovered after escalation" {
		t.Fatalf("unprofiled run unexpectedly continued: %q", result.FinalAnswer)
	}
}

// TestExecuteToolCallClassifiesRiskWithoutSandbox verifies the executed-risk
// stamp on the unsandboxed path: a read-only tool classifies low.
func TestExecuteToolCallClassifiesRiskWithoutSandbox(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(failingProfileTool{})

	result, abortErr := executeToolCall(
		context.Background(),
		registry,
		ToolCall{ID: "c1", Name: "flaky_probe"},
		PermissionModeAuto,
		Options{},
	)
	if abortErr != nil {
		t.Fatal(abortErr)
	}
	if result.Risk.Level == "" {
		t.Fatal("executed result must carry a risk classification")
	}
	if riskRank(result.Risk.Level) > riskRank(sandbox.RiskMedium) {
		t.Fatalf("read-only tool classified %q, want at most medium", result.Risk.Level)
	}
}

// TestDeniedToolResultCarriesZeroRisk verifies denial paths keep the zero value.
func TestDeniedToolResultCarriesZeroRisk(t *testing.T) {
	registry := tools.NewRegistry()
	result, abortErr := executeToolCall(
		context.Background(),
		registry,
		ToolCall{ID: "c1", Name: "no_such_tool"},
		PermissionModeAuto,
		Options{},
	)
	if abortErr != nil {
		t.Fatal(abortErr)
	}
	if result.DenialReason == DenialNone {
		t.Fatal("expected a denial for an unknown tool")
	}
	if result.Risk.Level != "" {
		t.Fatalf("denied result must keep the zero risk value, got %q", result.Risk.Level)
	}
}
