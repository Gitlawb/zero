package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestUnflattenToolArguments covers the Gemini-on-Copilot streaming quirk where
// nested tool arguments arrive as flattened dotted/bracketed keys, plus the
// conservative pass-through cases that must stay byte-for-byte unchanged.
func TestUnflattenToolArguments(t *testing.T) {
	t.Run("gemini flattened plan array rebuilds nested structure", func(t *testing.T) {
		in := `{"plan[0].content":"derive","plan[1].content":"run","plan[1].notes":"Execution step","plan[2].content":"verify"}`
		got := unflattenToolArguments(in)

		var parsed map[string]any
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unflattened output is not valid JSON: %v (%s)", err, got)
		}
		plan, ok := parsed["plan"].([]any)
		if !ok {
			t.Fatalf("plan is not an array: %#v", parsed["plan"])
		}
		if len(plan) != 3 {
			t.Fatalf("plan length = %d, want 3", len(plan))
		}
		step0 := plan[0].(map[string]any)
		if step0["content"] != "derive" {
			t.Fatalf("plan[0].content = %v, want derive", step0["content"])
		}
		step1 := plan[1].(map[string]any)
		if step1["content"] != "run" || step1["notes"] != "Execution step" {
			t.Fatalf("plan[1] = %#v, want content=run notes=Execution step", step1)
		}
		step2 := plan[2].(map[string]any)
		if step2["content"] != "verify" {
			t.Fatalf("plan[2].content = %v, want verify", step2["content"])
		}
	})

	t.Run("decodeToolArguments feeds a valid plan array to the tool", func(t *testing.T) {
		in := `{"plan[0].content":"derive","plan[1].content":"run"}`
		var args map[string]any
		if err := decodeToolArguments(in, &args); err != nil {
			t.Fatalf("decodeToolArguments: %v", err)
		}
		if _, ok := args["plan"].([]any); !ok {
			t.Fatalf("plan not decoded as array: %#v", args["plan"])
		}
	})

	t.Run("normal nested payload is untouched", func(t *testing.T) {
		in := `{"plan":[{"content":"derive"}]}`
		if got := unflattenToolArguments(in); got != in {
			t.Fatalf("normal payload changed: got %s", got)
		}
	})

	t.Run("flat non-nested payload is untouched", func(t *testing.T) {
		in := `{"path":"file.go","content":"data"}`
		if got := unflattenToolArguments(in); got != in {
			t.Fatalf("flat payload changed: got %s", got)
		}
	})

	t.Run("non-object payload is untouched", func(t *testing.T) {
		for _, in := range []string{`[1,2,3]`, `"hello"`, `not json`, ``} {
			if got := unflattenToolArguments(in); got != in {
				t.Fatalf("payload %q changed to %q", in, got)
			}
		}
	})

	t.Run("nested objects and deep paths rebuild correctly", func(t *testing.T) {
		in := `{"a.b.c":1,"a.d":2}`
		got := unflattenToolArguments(in)
		var parsed map[string]any
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		want := map[string]any{"a": map[string]any{"b": map[string]any{"c": float64(1)}, "d": float64(2)}}
		if !reflect.DeepEqual(parsed, want) {
			t.Fatalf("got %#v, want %#v", parsed, want)
		}
	})

	t.Run("structural conflict bails out and returns original", func(t *testing.T) {
		// "a" used as both a leaf and a container is unresolvable.
		in := `{"a":1,"a[0]":2}`
		if got := unflattenToolArguments(in); got != in {
			t.Fatalf("conflicting payload should be untouched, got %s", got)
		}
	})

	t.Run("oversized array index returns original", func(t *testing.T) {
		in := `{"plan[999999999].content":"x"}`
		if got := unflattenToolArguments(in); got != in {
			t.Fatalf("oversized index payload should be untouched, got %s", got)
		}
	})

	t.Run("cumulative sparse arrays return original", func(t *testing.T) {
		in := `{"a[10000]":1,"b[10000]":2}`
		if got := unflattenToolArguments(in); got != in {
			t.Fatalf("over-budget sparse arrays should be untouched, got %s", got)
		}
	})
}
