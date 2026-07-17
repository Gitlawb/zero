// Test seams: helpers only test code uses, kept out of the production binary.
package trace

import (
	"fmt"
	"io"
	"sort"
)

// WriteText emits a human-readable trace: a header, one line per span with its
// exclusive time and share of wall, a coverage line, then counters. It returns
// the first write error encountered so a failing sink (e.g. a full disk) is not
// silently swallowed.
func WriteText(w io.Writer, t *TurnTrace) error {
	if w == nil || t == nil {
		return nil
	}
	wall := t.WallDuration()
	var firstErr error
	write := func(format string, args ...any) {
		if firstErr != nil {
			return
		}
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			firstErr = err
		}
	}
	write("trace run=%s session=%s profile=%s\n", t.RunID, t.SessionID, t.Profile)
	write("  started=%s completed=%s wall=%s\n", formatTime(t.StartedAt), formatTime(t.CompletedAt), wall)
	write("  attributed=%s coverage=%.1f%%\n", t.AttributedDuration(), t.Coverage()*100)
	if !t.FirstVisibleEventAt.IsZero() {
		write("  first_visible_event=%s (+%s)\n", formatTime(t.FirstVisibleEventAt), t.FirstVisibleEventAt.Sub(t.StartedAt))
	}
	if !t.FirstUsefulActionAt.IsZero() {
		write("  first_useful_action=%s (+%s)\n", formatTime(t.FirstUsefulActionAt), t.FirstUsefulActionAt.Sub(t.StartedAt))
	}
	if !t.FirstTokenAt.IsZero() {
		write("  first_token=%s (+%s)\n", formatTime(t.FirstTokenAt), t.FirstTokenAt.Sub(t.StartedAt))
	}

	spans := append([]Span(nil), t.Spans...)
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Name != spans[j].Name {
			return spans[i].Name < spans[j].Name
		}
		return spans[i].Start.Before(spans[j].Start)
	})
	write("spans:\n")
	for _, span := range spans {
		share := 0.0
		if wall > 0 {
			share = float64(span.Exclusive) / float64(wall)
		}
		parent := ""
		if span.Parent != "" {
			parent = " [" + span.Parent + "]"
		}
		write("  %-18s %10s excl=%-10s %5.1f%%%s\n", span.Name, span.Duration, span.Exclusive, share*100, parent)
	}

	counters := append([]Counter(nil), t.Counters...)
	sort.Slice(counters, func(i, j int) bool { return counters[i].Name < counters[j].Name })
	write("counters:\n")
	for _, c := range counters {
		write("  %-22s %d\n", c.Name, c.Value)
	}
	if len(t.OutputBudgets) > 0 {
		write("output budgets:\n")
		for _, event := range t.OutputBudgets {
			write("  tool=%s category=%s bytes=%d/%d tokens=%d/%d truncated=%t reason=%s spill=%t\n",
				event.Tool, event.Category, event.RetainedBytes, event.OriginalBytes,
				event.EstimatedRetainedTokens, event.EstimatedOriginalTokens,
				event.Truncated, event.Reason, event.SpillCreated)
		}
	}
	if len(t.TaskStates) > 0 {
		latest := t.TaskStates[len(t.TaskStates)-1]
		write("task state: revision=%d status=%s plan=%d/%d/%d/%d tools=%d/%d verification=%d/%d(%s) files=%d plan_parity=%s completion=%s\n",
			latest.Revision, latest.Status, latest.PlanPending, latest.PlanInProgress,
			latest.PlanCompleted, latest.PlanFailed, latest.ToolsSucceeded,
			latest.ToolsFailed, latest.VerificationPassed, latest.VerificationFailed,
			latest.VerificationOutcome, latest.ChangedFileCount, latest.PlanParity,
			latest.CompletionDecision)
	}
	return firstErr
}
