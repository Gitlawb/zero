package sessions

import (
	"testing"
	"time"
)

func TestGoalLifecyclePersistsInSessionMetadata(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	store := NewStore(StoreOptions{
		RootDir: t.TempDir(),
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
	})
	session, err := store.Create(CreateInput{SessionID: "goal_session"})
	if err != nil {
		t.Fatal(err)
	}

	created, event, err := store.CreateGoal(session.SessionID, "Ship the release", 1_000)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventGoalCreated {
		t.Fatalf("create event = %q, want %q", event.Type, EventGoalCreated)
	}
	if created.Goal == nil || created.Goal.Objective != "Ship the release" ||
		created.Goal.Status != GoalStatusActive || created.Goal.TokenBudget != 1_000 {
		t.Fatalf("created goal = %#v", created.Goal)
	}

	accounted, _, err := store.AddGoalUsage(session.SessionID, 250)
	if err != nil {
		t.Fatal(err)
	}
	if accounted.Goal.TokensUsed != 250 || accounted.Goal.Status != GoalStatusActive {
		t.Fatalf("accounted goal = %#v", accounted.Goal)
	}

	paused, event, err := store.UpdateGoal(session.SessionID, GoalStatusPaused, "user interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventGoalUpdated || paused.Goal.Status != GoalStatusPaused ||
		paused.Goal.StatusReason != "user interrupted" {
		t.Fatalf("paused goal/event = %#v / %#v", paused.Goal, event)
	}

	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.Goal == nil || loaded.Goal.Status != GoalStatusPaused {
		t.Fatalf("reloaded session = %#v", loaded)
	}

	cleared, event, err := store.ClearGoal(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventGoalCleared || cleared.Goal != nil {
		t.Fatalf("cleared goal/event = %#v / %#v", cleared.Goal, event)
	}
}

func TestGoalBudgetPausesAtLimit(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "budget_session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateGoal(session.SessionID, "Stay bounded", 100); err != nil {
		t.Fatal(err)
	}

	updated, event, err := store.AddGoalUsage(session.SessionID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Goal.Status != GoalStatusBudgetLimited || updated.Goal.StatusReason != "token budget reached" {
		t.Fatalf("budgeted goal = %#v", updated.Goal)
	}
	if event == nil || event.Type != EventGoalUpdated {
		t.Fatalf("budget transition event = %#v", event)
	}
}

func TestEditGoalUpdatesStateAndRejectsInvalidInputWithoutMutation(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "edit_goal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateGoal(session.SessionID, "Original objective", 1_000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddGoalUsage(session.SessionID, 250); err != nil {
		t.Fatal(err)
	}

	edited, event, err := store.EditGoal(session.SessionID, "Updated objective", 500)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventGoalUpdated {
		t.Fatalf("edit event = %q, want %q", event.Type, EventGoalUpdated)
	}
	if edited.Goal == nil ||
		edited.Goal.Objective != "Updated objective" ||
		edited.Goal.TokenBudget != 500 ||
		edited.Goal.TokensUsed != 250 ||
		edited.Goal.Status != GoalStatusActive ||
		edited.Goal.StatusReason != "" {
		t.Fatalf("edited goal = %#v", edited.Goal)
	}

	limited, _, err := store.EditGoal(session.SessionID, "Stay within budget", 200)
	if err != nil {
		t.Fatal(err)
	}
	if limited.Goal == nil ||
		limited.Goal.Status != GoalStatusBudgetLimited ||
		limited.Goal.StatusReason != "token budget reached" ||
		limited.Goal.TokensUsed != 250 {
		t.Fatalf("budget-limited goal = %#v", limited.Goal)
	}

	before := *limited.Goal
	eventsBefore, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EditGoal(session.SessionID, "   ", 200); err == nil {
		t.Fatal("EditGoal should reject an empty objective")
	}
	if _, _, err := store.EditGoal(session.SessionID, "Invalid budget", -1); err == nil {
		t.Fatal("EditGoal should reject a negative token budget")
	}
	after, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if after == nil || after.Goal == nil || *after.Goal != before {
		t.Fatalf("invalid edit mutated goal: before=%#v after=%#v", before, after)
	}
	eventsAfter, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("invalid edit appended events: before=%d after=%d", len(eventsBefore), len(eventsAfter))
	}
}

func TestCreateGoalRefusesImplicitReplacement(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "replace_session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateGoal(session.SessionID, "First", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateGoal(session.SessionID, "Second", 0); err == nil {
		t.Fatal("CreateGoal should require an explicit clear before replacement")
	}
}

func TestPauseGoalIfActiveDoesNotOverwriteTerminalState(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "terminal_session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateGoal(session.SessionID, "Finish", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateGoal(session.SessionID, GoalStatusComplete, ""); err != nil {
		t.Fatal(err)
	}

	updated, event, err := store.PauseGoalIfActive(session.SessionID, "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if event != nil || updated.Goal.Status != GoalStatusComplete {
		t.Fatalf("terminal goal changed during cancellation: goal=%#v event=%#v", updated.Goal, event)
	}
}
