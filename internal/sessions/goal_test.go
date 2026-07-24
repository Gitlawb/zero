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
