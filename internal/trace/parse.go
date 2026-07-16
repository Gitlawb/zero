package trace

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// ReadNDJSON parses an NDJSON trace emitted by WriteNDJSON back into a TurnTrace.
// It is the inverse of WriteNDJSON and is used by the benchmark harness to turn a
// captured trace file into structured per-span stats.
//
// It fails loudly on a corrupt file rather than silently returning an empty
// trace. Empty or blank-only input is an error (an empty trace file means
// emission never happened — e.g. the agent crashed before writing the header
// or --trace was not honored — and must not masquerade as a valid zero-
// attribution sample). A non-empty input must contain a "type":"trace" header
// line; span/counter lines before it are an error; and a header that yields no
// spans and no counters is treated as corrupt. Individual span/counter lines
// with bad JSON are skipped only when a valid "trace" header has already been
// seen — a truncated middle of a real trace should not fatal a run.
//
// Numbers are decoded with UseNumber so counter values round-trip as exact
// int64s rather than going through float64 (which loses precision above 2^53).
func ReadNDJSON(r io.Reader) (*TurnTrace, error) {
	if r == nil {
		return nil, nil
	}
	t := &TurnTrace{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	sawInput := false
	sawTraceHeader := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		sawInput = true
		var obj map[string]any
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			// A non-JSON line before any header means the file is not a trace.
			if !sawTraceHeader {
				return nil, errors.New("parse trace: not a valid NDJSON trace (no type:trace header)")
			}
			continue
		}
		typ, _ := obj["type"].(string)
		switch typ {
		case "trace":
			sawTraceHeader = true
			t.RunID, _ = obj["run_id"].(string)
			t.SessionID, _ = obj["session_id"].(string)
			t.Profile, _ = obj["profile"].(string)
			t.StartedAt = parseTime(obj["started_at"])
			t.FirstVisibleEventAt = parseTime(obj["first_visible_at"])
			t.FirstUsefulActionAt = parseTime(obj["first_useful_at"])
			t.FirstTokenAt = parseTime(obj["first_token_at"])
			t.CompletedAt = parseTime(obj["completed_at"])
		case "span":
			if !sawTraceHeader {
				return nil, errors.New("parse trace: not a valid NDJSON trace (no type:trace header)")
			}
			name, _ := obj["name"].(string)
			s := Span{
				Name:     name,
				Start:    parseTime(obj["start"]),
				End:      parseTime(obj["end"]),
				Duration: parseDurationMs(obj["duration_ms"]),
			}
			if s.End.IsZero() && !s.Start.IsZero() {
				s.End = s.Start.Add(s.Duration)
			}
			// Preserve exclusive time exactly as written. A legitimately-zero
			// exclusive (a parent whose children cover its whole interval) is
			// emitted as exclusive_ms: 0 and MUST round-trip as 0 — falling back
			// to Duration here would re-introduce the double-counting the
			// exclusive-time model exists to prevent. Only fall back to Duration
			// when the key is genuinely absent (an older/duration-only emitter).
			if ev, ok := obj["exclusive_ms"]; ok {
				s.Exclusive = parseDurationMs(ev)
			} else {
				s.Exclusive = s.Duration
			}
			if v, ok := obj["parent"].(string); ok {
				s.Parent = v
			}
			t.Spans = append(t.Spans, s)
		case "counter":
			if !sawTraceHeader {
				return nil, errors.New("parse trace: not a valid NDJSON trace (no type:trace header)")
			}
			name, _ := obj["name"].(string)
			t.Counters = append(t.Counters, Counter{Name: name, Value: parseInt64(obj["value"])})
		default:
			// Unknown event type: tolerate (forward-compat) but only after a
			// header has been seen.
			if !sawTraceHeader {
				return nil, errors.New("parse trace: not a valid NDJSON trace (no type:trace header)")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !sawInput {
		// Empty or blank-only input: emission never produced a trace line, so
		// this is not a valid trace. Surface it so the harness records a
		// TraceIssue rather than treating a crashed run as clean zero-attribution.
		return nil, errors.New("parse trace: empty input (no trace emitted)")
	}
	if !sawTraceHeader {
		return nil, errors.New("parse trace: non-empty input had no type:trace header")
	}
	if len(t.Spans) == 0 && len(t.Counters) == 0 {
		return nil, errors.New("parse trace: header present but no spans or counters recovered (corrupt or truncated)")
	}
	return t, nil
}

func parseTime(v any) time.Time {
	s, _ := v.(string)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseDurationMs(v any) time.Duration {
	f := toFloat64(v)
	return time.Duration(f * float64(time.Millisecond))
}

// parseInt64 parses a counter value. json.Number (from UseNumber) is parsed
// directly to int64 so large counters round-trip without float64 precision
// loss; other numeric kinds fall back to a float conversion.
func parseInt64(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return int64(f)
		}
		return 0
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return int64(toFloat64(v))
	}
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
		return 0
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}
