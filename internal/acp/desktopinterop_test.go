package acp

// desktopinterop_test.go covers four defects found by driving the real `zero
// acp` binary from a desktop ACP client. Each is a case where ZERO and a
// conforming client disagreed about the wire, and none of them failed loudly —
// they produced a blank transcript, a silent denial, a spurious crash, or two
// buttons the user could not tell apart.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sessions"
)

// ---- session/load replays the conversation ----

// recordUpdates collects every session/update the agent sends, in order and
// with its variant, which the shared harness does not: that one keeps only
// agent_message_chunk text, and the whole question here is whether the USER's
// turns are replayed too.
type replayedUpdate struct {
	kind string
	text string
}

func recordUpdates(t *testing.T, deps Deps) (*clientHarness, chan replayedUpdate) {
	t.Helper()
	seen := make(chan replayedUpdate, 128)
	h := newHarness(t, deps)
	h.client.HandleNotify(MethodSessionUpdate, func(_ context.Context, params json.RawMessage) {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(params, &probe) != nil {
			return
		}
		seen <- replayedUpdate{kind: probe.Update.SessionUpdate, text: probe.Update.Content.Text}
	})
	return h, seen
}

func collect(t *testing.T, seen chan replayedUpdate, want int) []replayedUpdate {
	t.Helper()
	var got []replayedUpdate
	deadline := time.After(3 * time.Second)
	for len(got) < want {
		select {
		case update := <-seen:
			got = append(got, update)
		case <-deadline:
			return got
		}
	}
	return got
}

// A loaded session used to restore its turns into the agent's own memory and
// send nothing at all, so a client that resumed got an empty transcript and a
// model that remembered everything. Asking a follow-up produced a correct
// answer to a question no longer on screen.
func TestSessionLoadReplaysTheConversation(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	meta, err := deps.Store.Create(sessions.CreateInput{Title: "resumed", Cwd: cwd})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := deps.Store.AppendEvents(meta.SessionID, []sessions.AppendEventInput{
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "first question"}},
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "assistant", "content": "first answer"}},
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	h, seen := recordUpdates(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.Call(ctx, MethodSessionLoad,
		LoadSessionParams{SessionID: meta.SessionID, Cwd: cwd, McpServers: []McpServer{}},
		&LoadSessionResult{}); err != nil {
		t.Fatalf("session/load: %v", err)
	}

	got := collect(t, seen, 2)
	if len(got) < 2 {
		t.Fatalf("replayed %d updates, want 2: %+v", len(got), got)
	}
	// Order matters: the question has to arrive before its answer, or the
	// transcript reads backwards.
	if got[0].kind != UpdateUserMessageChunk || got[0].text != "first question" {
		t.Fatalf("first update = %+v, want the user's turn", got[0])
	}
	if got[1].kind != UpdateAgentMessageChunk || got[1].text != "first answer" {
		t.Fatalf("second update = %+v, want the agent's turn", got[1])
	}
}

// A session with nothing in it replays nothing, rather than an empty message
// pair that would render as two blank bubbles.
func TestSessionLoadOfAnEmptySessionReplaysNothing(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	meta, err := deps.Store.Create(sessions.CreateInput{Title: "empty", Cwd: cwd})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	h, seen := recordUpdates(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.Call(ctx, MethodSessionLoad,
		LoadSessionParams{SessionID: meta.SessionID, Cwd: cwd, McpServers: []McpServer{}},
		&LoadSessionResult{}); err != nil {
		t.Fatalf("session/load: %v", err)
	}

	select {
	case update := <-seen:
		t.Fatalf("empty session replayed %+v", update)
	case <-time.After(300 * time.Millisecond):
	}
}

// A turn persisted with a question but no answer — which is what a run
// interrupted mid-turn leaves behind — replays only the question. Inventing an
// empty assistant message for it would put a blank agent bubble on screen.
func TestSessionLoadSkipsAnAnswerThatWasNeverWritten(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	meta, err := deps.Store.Create(sessions.CreateInput{Title: "half", Cwd: cwd})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := deps.Store.AppendEvents(meta.SessionID, []sessions.AppendEventInput{
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "interrupted"}},
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	h, seen := recordUpdates(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.Call(ctx, MethodSessionLoad,
		LoadSessionParams{SessionID: meta.SessionID, Cwd: cwd, McpServers: []McpServer{}},
		&LoadSessionResult{}); err != nil {
		t.Fatalf("session/load: %v", err)
	}

	got := collect(t, seen, 2)
	if len(got) != 1 {
		t.Fatalf("replayed %d updates, want only the question: %+v", len(got), got)
	}
	if got[0].kind != UpdateUserMessageChunk {
		t.Fatalf("replayed %+v, want the user's turn alone", got[0])
	}
}

// ---- an offered option is an acceptable answer ----

// The list the options were BUILT from and the list the answer was CHECKED
// against were different whenever ZERO did not enumerate: the client was sent
// Allow and Reject, and its reply was validated against an empty slice, so
// every button it could possibly show failed closed to deny. The user clicked
// Allow and ZERO recorded a denial, with nothing on screen to say so.
func TestAnOfferedOptionIsAccepted(t *testing.T) {
	// No AvailableDecisions: the fallback path, which is every permission event
	// that is not a prompt.
	req := agent.PermissionRequest{ToolName: "bash"}

	options := buildPermissionOptions(req)
	if len(options) == 0 {
		t.Fatal("no options were offered")
	}

	for _, option := range options {
		decision := decisionFromOutcome(
			RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: option.OptionID},
			offeredDecisions(req),
		)
		if string(decision.Action) != option.OptionID {
			t.Fatalf("option %q was answered with %q (%s) — an offered option must be accepted",
				option.OptionID, decision.Action, decision.Reason)
		}
	}
}

// The narrowing this validation exists for still holds: a client must not be
// able to return a broader grant than it was shown.
func TestAnOptionThatWasNotOfferedIsStillRefused(t *testing.T) {
	req := agent.PermissionRequest{ToolName: "bash"}

	decision := decisionFromOutcome(
		RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: string(agent.PermissionDecisionAlwaysAllow)},
		offeredDecisions(req),
	)
	if decision.Action != agent.PermissionDecisionDeny {
		t.Fatalf("an unoffered broader grant was accepted as %q", decision.Action)
	}
}

// The resolver is the single source of truth for both sides, which is what
// makes the two agree by construction rather than by being kept in step.
func TestOfferedDecisionsMatchWhatWasSent(t *testing.T) {
	for _, req := range []agent.PermissionRequest{
		{ToolName: "bash"},
		{ToolName: "bash", AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowForSession,
			agent.PermissionDecisionDeny,
		}},
	} {
		offered := offeredDecisions(req)
		for _, option := range buildPermissionOptions(req) {
			if !actionOffered(agent.PermissionDecisionAction(option.OptionID), offered) {
				t.Fatalf("option %q was sent but is not in the offered set %v", option.OptionID, offered)
			}
		}
	}
}

// ---- two options the user can tell apart ----

// request_permissions offers plain allow and strict-review allow together, and
// both were labelled "Allow". The label is the only thing an ACP client shows,
// so the panel presented two identical buttons where one silently enabled
// strict auto-review of what was granted.
func TestEveryOfferedOptionHasADistinctLabel(t *testing.T) {
	req := agent.PermissionRequest{
		ToolName: "request_permissions",
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowStrict,
			agent.PermissionDecisionAllowForSession,
			agent.PermissionDecisionDeny,
		},
	}

	seen := map[string]string{}
	for _, option := range buildPermissionOptions(req) {
		if previous, clash := seen[option.Name]; clash {
			t.Fatalf("options %q and %q share the label %q — a user cannot tell them apart",
				previous, option.OptionID, option.Name)
		}
		seen[option.Name] = option.OptionID
	}
}

// The two remain distinct decisions: the label changed, the round trip did not.
func TestStrictAllowStillRoundTrips(t *testing.T) {
	req := agent.PermissionRequest{
		ToolName: "request_permissions",
		AvailableDecisions: []agent.PermissionDecisionAction{
			agent.PermissionDecisionAllow,
			agent.PermissionDecisionAllowStrict,
		},
	}
	decision := decisionFromOutcome(
		RequestPermissionOutcome{Outcome: OutcomeSelected, OptionID: string(agent.PermissionDecisionAllowStrict)},
		offeredDecisions(req),
	)
	if decision.Action != agent.PermissionDecisionAllowStrict {
		t.Fatalf("strict allow round-tripped as %q", decision.Action)
	}
}

// ---- cancelling a permission is a cancellation, not a crash ----

// Only context.Canceled was recognised, so dismissing a permission dialog came
// back as JSON-RPC -32603 carrying the internal sentinel text. Clients render
// that as a failed turn, so declining a tool looked like ZERO falling over —
// and for apply_patch, dismissing is the ONLY refusal a client is offered.
func TestCancellingAPermissionEndsTheTurnAsCancelled(t *testing.T) {
	canceled := fmt.Errorf("%w for apply_patch", agent.ErrPermissionApprovalCanceled)

	reason, err := stopReasonFor(agent.Result{}, canceled)
	if err != nil {
		t.Fatalf("stopReasonFor returned an error for a cancellation: %v", err)
	}
	if reason != StopCancelled {
		t.Fatalf("stop reason = %q, want %q", reason, StopCancelled)
	}
}

// A genuine failure must still be an error. Mapping too much to cancelled would
// hide real faults behind a clean ending, which is the opposite mistake.
func TestARealFailureIsStillAnError(t *testing.T) {
	failure := errors.New("provider exploded")

	if _, err := stopReasonFor(agent.Result{}, failure); !errors.Is(err, failure) {
		t.Fatalf("stopReasonFor(%v) = %v, want the failure preserved", failure, err)
	}
}

// The sentinel is matched through errors.Is, so it survives the wrapping every
// return site applies — each one adds the tool name.
func TestPermissionCancellationSurvivesWrapping(t *testing.T) {
	if !errors.Is(fmt.Errorf("%w for bash", agent.ErrPermissionApprovalCanceled), agent.ErrPermissionApprovalCanceled) {
		t.Fatal("a wrapped permission cancellation was not recognised")
	}
	if errors.Is(errors.New("something else"), agent.ErrPermissionApprovalCanceled) {
		t.Fatal("an unrelated error was reported as a permission cancellation")
	}
}
