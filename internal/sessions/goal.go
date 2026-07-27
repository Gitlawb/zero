package sessions

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	GoalObjectiveMaxLength            = 4_000
	GoalMaxConsecutiveContinuations   = 20
	goalContinuationLimitStatusReason = "automatic continuation limit reached"
)

func validateGoalObjective(objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("goal objective is required")
	}
	if utf8.RuneCountInString(objective) > GoalObjectiveMaxLength {
		return "", fmt.Errorf("goal objective cannot exceed %d characters", GoalObjectiveMaxLength)
	}
	return objective, nil
}

// CreateGoal persists a new active goal for a session. It refuses to replace an
// existing goal so callers must make replacement an explicit user decision.
func (store *Store) CreateGoal(sessionID, objective string, tokenBudget int) (Metadata, Event, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, Event{}, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	var err error
	objective, err = validateGoalObjective(objective)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	if tokenBudget < 0 {
		return Metadata{}, Event{}, fmt.Errorf("goal token budget cannot be negative")
	}

	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	if session.Goal != nil {
		return Metadata{}, Event{}, fmt.Errorf("session already has a goal")
	}
	now := store.timestamp()
	session.Goal = &Goal{
		Objective:         objective,
		Status:            GoalStatusActive,
		TokenBudget:       tokenBudget,
		ContinuationLimit: GoalMaxConsecutiveContinuations,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, Event{}, err
	}
	event, err := store.appendEventLocked(sessionID, AppendEventInput{
		Type:    EventGoalCreated,
		Payload: session.Goal,
	})
	if err != nil {
		return Metadata{}, Event{}, err
	}
	loaded, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	return loaded, event, nil
}

// UpdateGoal changes a goal's lifecycle state. Terminal goals remain available
// for status/history until the user explicitly clears them.
func (store *Store) UpdateGoal(sessionID string, status GoalStatus, reason string) (Metadata, Event, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, Event{}, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	if !validGoalStatus(status) {
		return Metadata{}, Event{}, fmt.Errorf("invalid goal status %q", status)
	}

	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	if session.Goal == nil {
		return Metadata{}, Event{}, fmt.Errorf("session has no goal")
	}
	session.Goal.Status = status
	session.Goal.StatusReason = strings.TrimSpace(reason)
	if status == GoalStatusActive {
		session.Goal.ContinuationCount = 0
		if session.Goal.ContinuationLimit <= 0 {
			session.Goal.ContinuationLimit = GoalMaxConsecutiveContinuations
		}
	}
	session.Goal.UpdatedAt = store.timestamp()
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, Event{}, err
	}
	event, err := store.appendEventLocked(sessionID, AppendEventInput{
		Type:    EventGoalUpdated,
		Payload: session.Goal,
	})
	if err != nil {
		return Metadata{}, Event{}, err
	}
	loaded, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	return loaded, event, nil
}

// EditGoal replaces the objective and optional budget without resetting usage or
// lifecycle timestamps. User-facing command handling owns confirmation.
func (store *Store) EditGoal(sessionID, objective string, tokenBudget int) (Metadata, Event, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, Event{}, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	var err error
	objective, err = validateGoalObjective(objective)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	if tokenBudget < 0 {
		return Metadata{}, Event{}, fmt.Errorf("goal token budget cannot be negative")
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	if session.Goal == nil {
		return Metadata{}, Event{}, fmt.Errorf("session has no goal")
	}
	session.Goal.Objective = objective
	session.Goal.TokenBudget = tokenBudget
	session.Goal.Status = GoalStatusActive
	session.Goal.StatusReason = ""
	session.Goal.ContinuationCount = 0
	if session.Goal.ContinuationLimit <= 0 {
		session.Goal.ContinuationLimit = GoalMaxConsecutiveContinuations
	}
	if tokenBudget > 0 && session.Goal.TokensUsed >= tokenBudget {
		session.Goal.Status = GoalStatusBudgetLimited
		session.Goal.StatusReason = "token budget reached"
	}
	session.Goal.UpdatedAt = store.timestamp()
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, Event{}, err
	}
	event, err := store.appendEventLocked(sessionID, AppendEventInput{
		Type:    EventGoalUpdated,
		Payload: session.Goal,
	})
	if err != nil {
		return Metadata{}, Event{}, err
	}
	loaded, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	return loaded, event, nil
}

// ResetGoalContinuations starts a fresh autonomous-run allowance after explicit
// user input. It makes the safety bound consecutive rather than lifetime-wide.
func (store *Store) ResetGoalContinuations(sessionID string) (Metadata, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, err
	}
	if session.Goal == nil || session.Goal.Status != GoalStatusActive {
		return session, nil
	}
	changed := false
	if session.Goal.ContinuationCount != 0 {
		session.Goal.ContinuationCount = 0
		changed = true
	}
	if session.Goal.ContinuationLimit <= 0 {
		session.Goal.ContinuationLimit = GoalMaxConsecutiveContinuations
		changed = true
	}
	if !changed {
		return session, nil
	}
	session.Goal.UpdatedAt = store.timestamp()
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, err
	}
	return session, nil
}

// ReserveGoalContinuation atomically reserves one automatic continuation. Once
// the persisted consecutive-run limit is exhausted it pauses the goal before
// another provider request can start, even when the provider reports no usage.
func (store *Store) ReserveGoalContinuation(sessionID string) (Metadata, *Event, bool, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, nil, false, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, nil, false, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, nil, false, err
	}
	if session.Goal == nil || session.Goal.Status != GoalStatusActive {
		return session, nil, false, nil
	}
	limit := session.Goal.ContinuationLimit
	if limit <= 0 {
		limit = GoalMaxConsecutiveContinuations
		session.Goal.ContinuationLimit = limit
	}
	if session.Goal.ContinuationCount < limit {
		session.Goal.ContinuationCount++
		session.Goal.UpdatedAt = store.timestamp()
		if err := store.writeMetadata(session); err != nil {
			return Metadata{}, nil, false, err
		}
		return session, nil, true, nil
	}

	session.Goal.Status = GoalStatusPaused
	session.Goal.StatusReason = goalContinuationLimitStatusReason
	session.Goal.UpdatedAt = store.timestamp()
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, nil, false, err
	}
	event, err := store.appendEventLocked(sessionID, AppendEventInput{
		Type:    EventGoalUpdated,
		Payload: session.Goal,
	})
	if err != nil {
		return Metadata{}, nil, false, err
	}
	loaded, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, nil, false, err
	}
	return loaded, &event, false, nil
}

// AddGoalUsage accounts tokens consumed while a goal is active. Reaching the
// optional budget pauses the goal before another autonomous turn can start.
func (store *Store) AddGoalUsage(sessionID string, tokens int) (Metadata, *Event, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, nil, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	if tokens < 0 {
		return Metadata{}, nil, fmt.Errorf("goal token usage cannot be negative")
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	if session.Goal == nil || tokens == 0 {
		return session, nil, nil
	}
	session.Goal.TokensUsed += tokens
	session.Goal.UpdatedAt = store.timestamp()
	budgetReached := false
	if session.Goal.Status == GoalStatusActive &&
		session.Goal.TokenBudget > 0 &&
		session.Goal.TokensUsed >= session.Goal.TokenBudget {
		session.Goal.Status = GoalStatusBudgetLimited
		session.Goal.StatusReason = "token budget reached"
		budgetReached = true
	}
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, nil, err
	}
	if !budgetReached {
		return session, nil, nil
	}
	event, err := store.appendEventLocked(sessionID, AppendEventInput{
		Type:    EventGoalUpdated,
		Payload: session.Goal,
	})
	if err != nil {
		return Metadata{}, nil, err
	}
	loaded, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	return loaded, &event, nil
}

// PauseGoalIfActive pauses a goal only while it is still active. It is used by
// cancellation paths where an in-flight agent may have completed or blocked the
// goal immediately before the cancellation reached the runtime.
func (store *Store) PauseGoalIfActive(sessionID, reason string) (Metadata, *Event, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, nil, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	if session.Goal == nil || session.Goal.Status != GoalStatusActive {
		return session, nil, nil
	}
	session.Goal.Status = GoalStatusPaused
	session.Goal.StatusReason = strings.TrimSpace(reason)
	session.Goal.UpdatedAt = store.timestamp()
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, nil, err
	}
	event, err := store.appendEventLocked(sessionID, AppendEventInput{
		Type:    EventGoalUpdated,
		Payload: session.Goal,
	})
	if err != nil {
		return Metadata{}, nil, err
	}
	loaded, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	return loaded, &event, nil
}

// ClearGoal removes the current goal while preserving an audit event.
func (store *Store) ClearGoal(sessionID string) (Metadata, Event, error) {
	if !ValidSessionID(sessionID) {
		return Metadata{}, Event{}, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	defer unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	if session.Goal == nil {
		return Metadata{}, Event{}, fmt.Errorf("session has no goal")
	}
	cleared := cloneGoal(session.Goal)
	session.Goal = nil
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, Event{}, err
	}
	event, err := store.appendEventLocked(sessionID, AppendEventInput{
		Type:    EventGoalCleared,
		Payload: cleared,
	})
	if err != nil {
		return Metadata{}, Event{}, err
	}
	loaded, err := store.readMetadata(sessionID)
	if err != nil {
		return Metadata{}, Event{}, err
	}
	return loaded, event, nil
}

func validGoalStatus(status GoalStatus) bool {
	switch status {
	case GoalStatusActive, GoalStatusPaused, GoalStatusBlocked, GoalStatusBudgetLimited, GoalStatusUsageLimited, GoalStatusComplete:
		return true
	default:
		return false
	}
}

func cloneGoal(goal *Goal) *Goal {
	if goal == nil {
		return nil
	}
	copy := *goal
	return &copy
}
