package trace

import (
	"encoding/json"
	"io"
	"sort"
	"time"
)

// WriteNDJSON emits the trace as newline-delimited JSON compatible with the
// internal/agenteval trace contract: one object per line carrying a "type"
// and (for spans/counters) a "name" so ParseTraceEventKeys keys them.
//
// The first line is a "trace" summary (name "run"), followed by one "span"
// line per span occurrence and one "counter" line per counter. Span lines
// carry the wall interval (start/end), inclusive duration, exclusive
// duration, and parent — the data the harness needs to rank latency sources
// without double-counting nested/concurrent work. Spans are emitted in stable
// (name, then start) order for deterministic output; counters are sorted.
func WriteNDJSON(w io.Writer, t *TurnTrace) error {
	if w == nil {
		return nil
	}
	if t == nil {
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(map[string]any{
		"type":             "trace",
		"name":             "run",
		"session_id":       t.SessionID,
		"run_id":           t.RunID,
		"profile":          t.Profile,
		"started_at":       formatTime(t.StartedAt),
		"first_visible_at": formatTime(t.FirstVisibleEventAt),
		"first_useful_at":  formatTime(t.FirstUsefulActionAt),
		"first_token_at":   formatTime(t.FirstTokenAt),
		"completed_at":     formatTime(t.CompletedAt),
		"wall_ms":          ms(t.WallDuration()),
		"attributed_ms":    ms(t.AttributedDuration()),
		"coverage":         round3(t.Coverage()),
		"attribution":      round3(t.AttributionRatio()),
	}); err != nil {
		return err
	}

	spans := append([]Span(nil), t.Spans...)
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Name != spans[j].Name {
			return spans[i].Name < spans[j].Name
		}
		return spans[i].Start.Before(spans[j].Start)
	})
	for _, span := range spans {
		obj := map[string]any{
			"type":         "span",
			"name":         span.Name,
			"duration_ms":  ms(span.Duration),
			"exclusive_ms": ms(span.Exclusive),
		}
		if !span.Start.IsZero() {
			obj["start"] = formatTime(span.Start)
		}
		if !span.End.IsZero() {
			obj["end"] = formatTime(span.End)
		}
		if span.Parent != "" {
			obj["parent"] = span.Parent
		}
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}

	counters := append([]Counter(nil), t.Counters...)
	sort.Slice(counters, func(i, j int) bool { return counters[i].Name < counters[j].Name })
	for _, c := range counters {
		if err := enc.Encode(map[string]any{
			"type":  "counter",
			"name":  c.Name,
			"value": c.Value,
		}); err != nil {
			return err
		}
	}

	// Prefix fingerprints are emitted after counters in insertion (turn)
	// order. The order is the order EmitPrefixHash was called, which is the
	// order the agent loop computed each turn's fingerprint, which is the
	// order a downstream consumer needs to correlate a prefix_hash event
	// with the cached_input_tokens counter for that turn. Sorting by
	// complete_prefix hash would destroy that correlation, so we do not
	// sort. The slice is already a deep copy from Finish (see
	// Recorder.Finish) so it is safe to range over without copying.
	for _, p := range t.PrefixHashes {
		if err := enc.Encode(map[string]any{
			"type":                "prefix_hash",
			"system_prompt":       p.SystemPromptHash,
			"base_instructions":   p.BaseInstructionsHash,
			"confirmation_policy": p.ConfirmationPolicyHash,
			"project_context":     p.ProjectContextHash,
			"skills":              p.SkillsHash,
			"tools":               p.ToolsHash,
			"schema":              p.SchemaHash,
			"complete_prefix":     p.CompletePrefixHash,
		}); err != nil {
			return err
		}
	}
	// Output budget events stay in tool-result emission order. Sorting them by
	// tool/category would destroy correlation with concurrent calls whose results
	// are deliberately consumed in original call order.
	for _, event := range t.OutputBudgets {
		if err := enc.Encode(map[string]any{
			"type":                      "output_budget",
			"tool":                      event.Tool,
			"category":                  event.Category,
			"original_bytes":            event.OriginalBytes,
			"retained_bytes":            event.RetainedBytes,
			"estimated_original_tokens": event.EstimatedOriginalTokens,
			"estimated_retained_tokens": event.EstimatedRetainedTokens,
			"truncated":                 event.Truncated,
			"reason":                    event.Reason,
			"spill_created":             event.SpillCreated,
		}); err != nil {
			return err
		}
	}
	for _, event := range t.TaskStates {
		if err := enc.Encode(map[string]any{
			"type":                 "task_state",
			"revision":             event.Revision,
			"status":               event.Status,
			"plan_pending":         event.PlanPending,
			"plan_in_progress":     event.PlanInProgress,
			"plan_completed":       event.PlanCompleted,
			"plan_failed":          event.PlanFailed,
			"tools_succeeded":      event.ToolsSucceeded,
			"tools_failed":         event.ToolsFailed,
			"verification_passed":  event.VerificationPassed,
			"verification_failed":  event.VerificationFailed,
			"verification_outcome": event.VerificationOutcome,
			"changed_file_count":   event.ChangedFileCount,
			"completion_decision":  event.CompletionDecision,
			"plan_parity":          event.PlanParity,
		}); err != nil {
			return err
		}
	}
	return nil
}

// An OpenTelemetry export path is a documented future addition. It is
// intentionally not implemented in the baseline: doing so would pull in the
// OTLP exporter dependency. When added, mirror WriteNDJSON by translating
// each Span into an OTLP span and each Counter into an attribute, parented
// under the run's trace.

func ms(d time.Duration) float64 { return round3(float64(d.Microseconds()) / 1000) }

func round3(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
