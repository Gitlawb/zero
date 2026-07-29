package specialist

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Gitlawb/zero/internal/sessions"
)

// Resume: what a plan had already done when the process went away.
//
// This is the READ half of durability. The write half put the five lifecycle
// events in the session log from both surfaces; this reduces them back into
// plan state. ARCHITECTURE.md's rule is the shape of the whole thing — plan
// state is a DETERMINISTIC REDUCTION over the five events, and there is no
// second store to disagree with.
//
// WHAT THE EVENTS DO AND DO NOT CARRY. The payloads are deliberately small: ids,
// counts, durations, terminal status — not prompts. So the log says which tasks
// finished; it cannot say what they were asked to do. That is why resuming is
// built on a SAVED plan: the plan supplies the work, the log supplies the
// progress, and neither has to grow into the other. Putting prompts in the log
// would have multiplied a fifty-task plan's on-disk size to make the log a
// second copy of something already stored better.
//
// A DISPATCH WITHOUT A TERMINAL EVENT IS THE POINT. Dispatch is written before
// the child runs, so a task that was in flight when the process died is
// distinguishable from one that never started — and it is treated as UNFINISHED,
// never as done. Assuming otherwise would silently drop the one task most likely
// to have been interrupted mid-work.

// PlanProgress is the state of one plan, reduced from its events.
type PlanProgress struct {
	// Name is the plan's name from plan_admitted.
	Name string
	// Order is the execution order recorded at admission.
	Order []string
	// Succeeded holds the ids that reached task_completed.
	Succeeded []string
	// Unfinished holds ids that were dispatched and never reached a terminal
	// event — the process went away mid-task.
	Unfinished []string
	// Failed holds ids that reached task_failed, whatever the outcome. A failed
	// task is worth RE-RUNNING: unlike a success it produced no work to keep.
	Failed []string
	// Complete reports that a plan_completed event was seen, so the plan ended
	// on purpose rather than being cut off.
	Complete bool
	// Status is the terminal status from plan_completed, empty if there was none.
	Status string
}

// Done reports whether there is anything left to run.
func (progress PlanProgress) Done() bool {
	return len(progress.Unfinished) == 0 && len(progress.Failed) == 0 &&
		len(progress.Succeeded) == len(progress.Order)
}

// Remaining lists the ids that still need to run, in the recorded execution
// order — everything that did not succeed.
func (progress PlanProgress) Remaining() []string {
	succeeded := map[string]bool{}
	for _, id := range progress.Succeeded {
		succeeded[id] = true
	}
	out := []string{}
	for _, id := range progress.Order {
		if !succeeded[id] {
			out = append(out, id)
		}
	}
	return out
}

// ReducePlanEvents folds a session's events into the state of its LAST plan.
//
// The last one, deliberately: a session can run several plans, and "resume"
// means the one that was interrupted, which is the most recent. plan_admitted
// RESETS the accumulated state rather than merging into it, so a second plan
// never inherits the first plan's completed tasks — which would mark work as
// done that this plan never did.
func ReducePlanEvents(events []sessions.Event) (PlanProgress, bool) {
	var progress PlanProgress
	seen := false

	// Sequence order, not file order: the reduction has to be deterministic and
	// events are only meaningful in the order they happened.
	ordered := append([]sessions.Event(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })

	dispatched := map[string]bool{}
	terminal := map[string]bool{}
	for _, event := range ordered {
		switch event.Type {
		case sessions.EventPlanAdmitted:
			var payload struct {
				Name  string   `json:"name"`
				Order []string `json:"order"`
			}
			if json.Unmarshal(event.Payload, &payload) != nil {
				// Malformed is not a silent skip: an admission we cannot read
				// means we do not know this plan's shape, so the previous plan's
				// state must not be reported as if it were this one's.
				return PlanProgress{}, false
			}
			seen = true
			progress = PlanProgress{Name: payload.Name, Order: payload.Order}
			dispatched = map[string]bool{}
			terminal = map[string]bool{}
		case sessions.EventTaskDispatched:
			if id := planEventTaskID(event); id != "" {
				dispatched[id] = true
			}
		case sessions.EventTaskCompleted:
			if id := planEventTaskID(event); id != "" {
				terminal[id] = true
				progress.Succeeded = append(progress.Succeeded, id)
			}
		case sessions.EventTaskFailed:
			if id := planEventTaskID(event); id != "" {
				terminal[id] = true
				progress.Failed = append(progress.Failed, id)
			}
		case sessions.EventPlanCompleted:
			var payload struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			progress.Complete = true
			progress.Status = payload.Status
		}
	}
	if !seen {
		return PlanProgress{}, false
	}
	// Dispatched with no terminal event: in flight when the process went away.
	for _, id := range progress.Order {
		if dispatched[id] && !terminal[id] {
			progress.Unfinished = append(progress.Unfinished, id)
		}
	}
	return progress, true
}

func planEventTaskID(event sessions.Event) string {
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil {
		return ""
	}
	return payload.ID
}

// RemainingPlan narrows a plan to the work that has not succeeded.
//
// A completed task is REMOVED, and every reference to it is removed with it.
// Leaving the edge behind would produce a plan whose dependency names nothing —
// ParsePlan refuses that, correctly — and rewriting the edge to point at the
// next task along would invent an ordering nobody declared. A dependency on
// work that is already done is simply satisfied.
//
// The result goes back through ParsePlan, so a narrowed plan is validated like
// any other: it cannot acquire a tool, a task count or a depth the original did
// not have, and a narrowing that produced a cycle would be caught rather than
// executed.
func RemainingPlan(plan Plan, progress PlanProgress, limits Limits) (Plan, error) {
	succeeded := map[string]bool{}
	for _, id := range progress.Succeeded {
		succeeded[id] = true
	}

	args := plan.Args()
	rawTasks, _ := args["tasks"].([]any)
	kept := make([]any, 0, len(rawTasks))
	for _, raw := range rawTasks {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		if succeeded[id] {
			continue
		}
		if deps, ok := entry["depends_on"].([]any); ok {
			remaining := make([]any, 0, len(deps))
			for _, dep := range deps {
				name, _ := dep.(string)
				if succeeded[name] {
					continue
				}
				remaining = append(remaining, dep)
			}
			if len(remaining) == 0 {
				delete(entry, "depends_on")
			} else {
				entry["depends_on"] = remaining
			}
		}
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		return Plan{}, fmt.Errorf("every task in plan %q already succeeded; there is nothing left to run", plan.Name())
	}
	args["tasks"] = kept
	return ParsePlan(args, limits)
}
