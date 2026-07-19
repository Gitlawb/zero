package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// Sink is the abstraction a finished TurnTrace is written to. The NDJSON and
// text sinks are implemented here; an OpenTelemetry sink is a documented
// future addition (see opentelemetrySink below) and is intentionally not
// pulled in as a dependency.
type Sink interface {
	Emit(*TurnTrace) error
}

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
	return nil
}

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
	return firstErr
}

// NDJSONSink adapts an io.Writer as a Sink emitting NDJSON.
type NDJSONSink struct{ W io.Writer }

func (s NDJSONSink) Emit(t *TurnTrace) error { return WriteNDJSON(s.W, t) }

// TextSink adapts an io.Writer as a Sink emitting human-readable text.
type TextSink struct{ W io.Writer }

func (s TextSink) Emit(t *TurnTrace) error { return WriteText(s.W, t) }

// opentelemetrySink is a placeholder documenting the future OpenTelemetry
// export path. It is intentionally not implemented in the baseline: doing so
// would pull in the OTLP exporter dependency. When added, satisfy Sink by
// translating each Span into an OTLP span and each Counter into an attribute,
// parented under the run's trace:
//
//	type opentelemetrySink struct{ exp someExporter }
//	func (s opentelemetrySink) Emit(t *TurnTrace) error { ... }
//
// It is left as a comment to avoid an unused-type lint while signaling the
// intended extension seam to the next PR.

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
