//go:build windows

package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gitlawb/zero/internal/execution"
)

// A REPORT THAT SAYS A CHILD LAUNCHED HAS TO MEAN THE CHILD COULD RUN.
//
// The report is published before ResumeThread so the inherited-pipe race is
// closed, and that ordering is right. But a failure between the publish and the
// resume reaps a process that executed nothing, and the report was left on disk
// saying true. AppliedEnforcementNotices gates on that report, so the operator
// would have been told a write-jail trade applied to a child that never became
// runnable. The record has to be unwound on that path, not only on the ones
// before it was written.
func TestResumeFailureLeavesNoLaunchReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report, err := openWindowsExecutionReport(path)
	if err != nil {
		t.Fatal(err)
	}

	published, err := publishThenResume(report, func() error { return errors.New("STATUS_ACCESS_DENIED") })
	if err == nil {
		t.Fatal("SETUP INVALID: the injected resume failure was not reported")
	}
	if published {
		t.Fatal("publishThenResume reported the child as published after its resume failed, so the deferred close would keep a report saying true about a child that never ran")
	}
	report.close(published)

	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("a launch report survived a resume failure (stat: %v); the parent would read a child as launched that executed no instruction", statErr)
	}
}

// And the ordinary path still publishes exactly what the parent relies on.
func TestSuccessfulResumeKeepsTheLaunchReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report, err := openWindowsExecutionReport(path)
	if err != nil {
		t.Fatal(err)
	}

	published, err := publishThenResume(report, func() error { return nil })
	if err != nil || !published {
		t.Fatalf("publishThenResume = (%v, %v), want (true, nil)", published, err)
	}
	report.close(published)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the launch report is gone after a successful resume: %v", err)
	}
	var decoded execution.AdapterReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if decoded.ChildLaunched == nil || !*decoded.ChildLaunched {
		t.Fatalf("report = %s, want childLaunched true", data)
	}
}
