package specialist

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

func TestParseStreamAndBuildFinalResultSuccess(t *testing.T) {
	events, err := ParseStream(strings.NewReader(strings.Join([]string{
		`{"schemaVersion":1,"type":"run_start","runId":"run_1","sessionId":"child","cwd":"/repo"}`,
		`{"schemaVersion":1,"type":"text","runId":"run_1","delta":"part "}`,
		`{"schemaVersion":1,"type":"text","runId":"run_1","delta":"one"}`,
		`{"schemaVersion":1,"type":"tool_call","runId":"run_1","id":"call_1","name":"grep"}`,
		`{"schemaVersion":1,"type":"final","runId":"run_1","text":"done"}`,
		`{"schemaVersion":1,"type":"run_end","runId":"run_1","status":"success","exitCode":0}`,
		"",
	}, "\n")))
	if err != nil {
		t.Fatalf("ParseStream returned error: %v", err)
	}

	summary := SummarizeStream(events, 0)
	if summary.SessionID != "child" || summary.Text != "done" || summary.ExitCode != 0 || len(summary.Tools) != 1 || summary.Tools[0] != "grep" {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	result := BuildFinalResult(events, "", 0)
	if result.Status != tools.StatusOK || result.Output != "session_id: child\ndone" {
		t.Fatalf("unexpected final result: %#v", result)
	}
}

func TestBuildFinalResultUsesTextDeltasWhenFinalMissing(t *testing.T) {
	events, err := ParseStream(strings.NewReader(strings.Join([]string{
		`{"schemaVersion":1,"type":"run_start","runId":"run_1","sessionId":"child"}`,
		`{"schemaVersion":1,"type":"text","runId":"run_1","delta":"hello"}`,
		`{"schemaVersion":1,"type":"text","runId":"run_1","delta":" world"}`,
		"",
	}, "\n")))
	if err != nil {
		t.Fatalf("ParseStream returned error: %v", err)
	}
	result := BuildFinalResult(events, "", 0)
	if result.Status != tools.StatusOK || result.Output != "session_id: child\nhello world" {
		t.Fatalf("unexpected final result: %#v", result)
	}
}

func TestBuildFinalResultErrorIncludesDiagnostics(t *testing.T) {
	events, err := ParseStream(strings.NewReader(strings.Join([]string{
		`{"schemaVersion":1,"type":"run_start","runId":"run_1","sessionId":"child"}`,
		`{"schemaVersion":1,"type":"tool_call","runId":"run_1","id":"call_1","name":"bash"}`,
		`{"schemaVersion":1,"type":"error","runId":"run_1","code":"provider_error","message":"model failed"}`,
		`{"schemaVersion":1,"type":"run_end","runId":"run_1","status":"error","exitCode":3}`,
		"",
	}, "\n")))
	if err != nil {
		t.Fatalf("ParseStream returned error: %v", err)
	}
	result := BuildFinalResult(events, "stderr text", 0)
	if result.Status != tools.StatusError {
		t.Fatalf("Status = %s, want error", result.Status)
	}
	for _, want := range []string{"Subagent failed (exit 3)", "model failed", "stderr text", "tools executed: bash"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("error output missing %q:\n%s", want, result.Output)
		}
	}
}

func TestParseStreamRejectsInvalidLines(t *testing.T) {
	_, err := ParseStream(strings.NewReader(`{"schemaVersion":1,"runId":"run_1"}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "type is required") {
		t.Fatalf("expected missing type error, got %v", err)
	}

	_, err = ParseStream(strings.NewReader(`not-json` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "parse stream-json line 1") {
		t.Fatalf("expected json parse error, got %v", err)
	}
}
