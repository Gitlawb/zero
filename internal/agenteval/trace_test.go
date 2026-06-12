package agenteval

import (
	"reflect"
	"testing"
)

func TestParseTraceEventKeysIgnoresNoiseAndNormalizesJSONLines(t *testing.T) {
	stdout := "starting agent\n" +
		"{\"type\":\"tool\",\"name\":\"read_file\"}\n" +
		"{\"event\":\"verify\",\"name\":\"go-test\"}\n" +
		"{\"type\":\"finish\"}\n" +
		"{\"event\":\"verify\"}\n" +
		"not-json\n"

	got := ParseTraceEventKeys(stdout)
	want := []string{"finish", "tool:read_file", "verify", "verify:go-test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event keys = %#v, want %#v", got, want)
	}
}

func TestMissingTraceEventsReturnsSortedMissingRequiredEvents(t *testing.T) {
	stdout := "{\"type\":\"tool\",\"name\":\"read_file\"}\n"

	got := MissingTraceEvents([]string{"tool:apply_patch", "tool:read_file", "verify:go-test"}, stdout)
	want := []string{"tool:apply_patch", "verify:go-test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missing events = %#v, want %#v", got, want)
	}
}
