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
// trace: a non-empty input must contain a "type":"trace" header line, and a
// non-empty input that yields no spans and no counters is treated as corrupt.
// Individual span/counter lines with bad JSON are skipped only when a valid
// "trace" header has already been seen — a truncated middle of a real trace
// should not fatal a run, but a file with no header at all is never parsed as
// a valid empty trace.
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
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
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
			s.Exclusive = parseDurationMs(obj["exclusive_ms"])
			if s.Exclusive <= 0 {
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
	if sawInput && !sawTraceHeader {
		return nil, errors.New("parse trace: non-empty input had no type:trace header")
	}
	if sawTraceHeader && len(t.Spans) == 0 && len(t.Counters) == 0 {
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

func parseInt64(v any) int64 {
	return int64(toFloat64(v))
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
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
