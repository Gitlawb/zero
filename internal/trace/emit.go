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
// internal/agenteval trace contract: one object per line, each carrying a
// "type" and (for spans/counters) a "name" so ParseTraceEventKeys keys them.
//
// The first line is a "trace" summary (name "run"), followed by one "span"
// line per span and one "counter" line per counter. Spans and counters are
// emitted in stable (sorted) order for deterministic output.
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
		"attribution":      round3(t.AttributionRatio()),
	}); err != nil {
		return err
	}

	spans := append([]Span(nil), t.Spans...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].Name < spans[j].Name })
	for _, span := range spans {
		if err := enc.Encode(map[string]any{
			"type":        "span",
			"name":        span.Name,
			"duration_ms": ms(span.Duration),
		}); err != nil {
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
	return nil
}

// WriteText emits a human-readable trace: a header, one line per span with its
// share of wall time, a totals line, then counters. The first write error is
// returned; remaining writes are skipped so a failing sink surfaces promptly
// instead of being silently dropped.
func WriteText(w io.Writer, t *TurnTrace) error {
	if w == nil || t == nil {
		return nil
	}
	wall := t.WallDuration()
	var p textPrinter
	p.printf(w, "trace run=%s session=%s profile=%s\n", t.RunID, t.SessionID, t.Profile)
	p.printf(w, "  started=%s completed=%s wall=%s\n", formatTime(t.StartedAt), formatTime(t.CompletedAt), wall)
	if !t.FirstVisibleEventAt.IsZero() {
		p.printf(w, "  first_visible_event=%s (+%s)\n", formatTime(t.FirstVisibleEventAt), t.FirstVisibleEventAt.Sub(t.StartedAt))
	}
	if !t.FirstUsefulActionAt.IsZero() {
		p.printf(w, "  first_useful_action=%s (+%s)\n", formatTime(t.FirstUsefulActionAt), t.FirstUsefulActionAt.Sub(t.StartedAt))
	}
	if !t.FirstTokenAt.IsZero() {
		p.printf(w, "  first_token=%s (+%s)\n", formatTime(t.FirstTokenAt), t.FirstTokenAt.Sub(t.StartedAt))
	}

	spans := append([]Span(nil), t.Spans...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].Name < spans[j].Name })
	p.println(w, "spans:")
	for _, span := range spans {
		share := 0.0
		if wall > 0 {
			share = float64(span.Duration) / float64(wall)
		}
		p.printf(w, "  %-18s %10s  %5.1f%%\n", span.Name, span.Duration, share*100)
	}
	p.printf(w, "  %-18s %10s  %5.1f%%\n", "attributed", t.AttributedDuration(), t.AttributionRatio()*100)

	counters := append([]Counter(nil), t.Counters...)
	sort.Slice(counters, func(i, j int) bool { return counters[i].Name < counters[j].Name })
	p.println(w, "counters:")
	for _, c := range counters {
		p.printf(w, "  %-22s %d\n", c.Name, c.Value)
	}
	return p.err
}

// textPrinter remembers the first write error so a stream of fprintf/fprintln
// calls can short-circuit after the sink fails, then report it once.
type textPrinter struct{ err error }

func (p *textPrinter) printf(w io.Writer, format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(w, format, args...)
}

func (p *textPrinter) println(w io.Writer, s string) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintln(w, s)
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
