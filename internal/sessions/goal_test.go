package sessions

import (
	"os"
	"strings"
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

func TestGoalContinuationLimitStopsWithoutProviderUsage(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "continuation_limit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateGoal(session.SessionID, "Stay bounded without usage events", 0); err != nil {
		t.Fatal(err)
	}

	for continuation := 1; continuation <= GoalMaxConsecutiveContinuations; continuation++ {
		updated, event, reserved, err := store.ReserveGoalContinuation(session.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if !reserved || event != nil {
			t.Fatalf("continuation %d: reserved=%v event=%#v", continuation, reserved, event)
		}
		if updated.Goal.ContinuationCount != continuation {
			t.Fatalf("continuation count = %d, want %d", updated.Goal.ContinuationCount, continuation)
		}
		if updated.Goal.TokensUsed != 0 {
			t.Fatalf("provider-independent guard unexpectedly recorded tokens: %#v", updated.Goal)
		}
	}

	stopped, event, reserved, err := store.ReserveGoalContinuation(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if reserved || event == nil || stopped.Goal.Status != GoalStatusPaused ||
		stopped.Goal.StatusReason != goalContinuationLimitStatusReason {
		t.Fatalf("unbounded continuation was not stopped: goal=%#v event=%#v reserved=%v", stopped.Goal, event, reserved)
	}
	reloaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Goal.ContinuationCount != GoalMaxConsecutiveContinuations ||
		reloaded.Goal.Status != GoalStatusPaused {
		t.Fatalf("continuation guard did not persist: %#v", reloaded.Goal)
	}

	resumed, _, err := store.UpdateGoal(session.SessionID, GoalStatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Goal.ContinuationCount != 0 {
		t.Fatalf("explicit resume did not reset consecutive continuations: %#v", resumed.Goal)
	}
}

func TestResetGoalContinuationsPersistsOnlyActiveChanges(t *testing.T) {
	newStore := func(t *testing.T) (*Store, Metadata) {
		t.Helper()
		now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
		store := NewStore(StoreOptions{
			RootDir: t.TempDir(),
			Now: func() time.Time {
				now = now.Add(time.Second)
				return now
			},
		})
		session, err := store.Create(CreateInput{SessionID: "reset_goal"})
		if err != nil {
			t.Fatal(err)
		}
		return store, session
	}
	pinMetadataTime := func(t *testing.T, store *Store, sessionID string) time.Time {
		t.Helper()
		path := store.metadataPath(sessionID)
		sentinel := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
		if err := os.Chtimes(path, sentinel, sentinel); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.ModTime()
	}
	assertMetadataTime := func(t *testing.T, store *Store, sessionID string, want time.Time) {
		t.Helper()
		info, err := os.Stat(store.metadataPath(sessionID))
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(want) {
			t.Fatalf("metadata was rewritten: modtime=%s want=%s", info.ModTime(), want)
		}
	}

	t.Run("no goal is a no-op", func(t *testing.T) {
		store, session := newStore(t)
		modTime := pinMetadataTime(t, store, session.SessionID)
		updated, err := store.ResetGoalContinuations(session.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Goal != nil {
			t.Fatalf("no-goal reset created a goal: %#v", updated.Goal)
		}
		assertMetadataTime(t, store, session.SessionID, modTime)
	})

	t.Run("inactive goal is a no-op", func(t *testing.T) {
		store, session := newStore(t)
		if _, _, err := store.CreateGoal(session.SessionID, "Pause safely", 0); err != nil {
			t.Fatal(err)
		}
		if _, _, reserved, err := store.ReserveGoalContinuation(session.SessionID); err != nil || !reserved {
			t.Fatalf("reserve continuation: reserved=%v err=%v", reserved, err)
		}
		paused, _, err := store.UpdateGoal(session.SessionID, GoalStatusPaused, "user paused")
		if err != nil {
			t.Fatal(err)
		}
		modTime := pinMetadataTime(t, store, session.SessionID)
		updated, err := store.ResetGoalContinuations(session.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Goal.ContinuationCount != 1 || updated.Goal.UpdatedAt != paused.Goal.UpdatedAt {
			t.Fatalf("inactive goal changed during reset: before=%#v after=%#v", paused.Goal, updated.Goal)
		}
		assertMetadataTime(t, store, session.SessionID, modTime)
	})

	t.Run("already reset active goal is a no-op", func(t *testing.T) {
		store, session := newStore(t)
		created, _, err := store.CreateGoal(session.SessionID, "Stay reset", 0)
		if err != nil {
			t.Fatal(err)
		}
		modTime := pinMetadataTime(t, store, session.SessionID)
		updated, err := store.ResetGoalContinuations(session.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Goal.ContinuationCount != 0 ||
			updated.Goal.ContinuationLimit != GoalMaxConsecutiveContinuations ||
			updated.Goal.UpdatedAt != created.Goal.UpdatedAt {
			t.Fatalf("already-reset goal changed: before=%#v after=%#v", created.Goal, updated.Goal)
		}
		assertMetadataTime(t, store, session.SessionID, modTime)
	})

	t.Run("active goal reset persists", func(t *testing.T) {
		store, session := newStore(t)
		if _, _, err := store.CreateGoal(session.SessionID, "Reset progress", 0); err != nil {
			t.Fatal(err)
		}
		reserved, _, ok, err := store.ReserveGoalContinuation(session.SessionID)
		if err != nil || !ok {
			t.Fatalf("reserve continuation: reserved=%v err=%v", ok, err)
		}
		updated, err := store.ResetGoalContinuations(session.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Goal.ContinuationCount != 0 ||
			updated.Goal.ContinuationLimit != GoalMaxConsecutiveContinuations ||
			updated.Goal.UpdatedAt == reserved.Goal.UpdatedAt {
			t.Fatalf("active reset did not persist: before=%#v after=%#v", reserved.Goal, updated.Goal)
		}
		reloaded, err := store.Get(session.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Goal == nil || *reloaded.Goal != *updated.Goal {
			t.Fatalf("active reset was not durable: updated=%#v reloaded=%#v", updated.Goal, reloaded.Goal)
		}
	})
}

func TestGoalObjectiveLengthIsBounded(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "objective_limit"})
	if err != nil {
		t.Fatal(err)
	}
	tooLong := strings.Repeat("g", GoalObjectiveMaxLength+1)
	if _, _, err := store.CreateGoal(session.SessionID, tooLong, 0); err == nil ||
		!strings.Contains(err.Error(), "cannot exceed") {
		t.Fatalf("oversized objective error = %v", err)
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
