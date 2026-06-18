package agent

import (
	"strings"
	"testing"
)

func TestSpecialistDelegationContext(t *testing.T) {
	if got := specialistDelegationContext(Options{}); got != "" {
		t.Fatalf("no specialists should yield an empty section, got %q", got)
	}
	got := specialistDelegationContext(Options{Specialists: []SpecialistInfo{
		{Name: "explorer", WhenToUse: "Read-only codebase exploration."},
		{Name: "  ", WhenToUse: "nameless, should be skipped"},
		{Name: "worker"},
	}})
	if !strings.Contains(got, "<specialists>") || !strings.Contains(got, "</specialists>") {
		t.Fatalf("missing specialists block: %q", got)
	}
	if !strings.Contains(got, "- explorer: Read-only codebase exploration.") {
		t.Fatalf("missing explorer line: %q", got)
	}
	if !strings.Contains(got, "- worker\n") {
		t.Fatalf("worker (no description) line missing: %q", got)
	}
	if strings.Contains(got, "nameless") {
		t.Fatalf("nameless entry should be skipped: %q", got)
	}
}

func TestSystemPromptIncludesDelegationOnlyWithSpecialists(t *testing.T) {
	with := buildSystemPrompt(Options{Specialists: []SpecialistInfo{
		{Name: "explorer", WhenToUse: "Explore."},
	}})
	if !strings.Contains(with, "<specialists>") || !strings.Contains(with, "Task tool") {
		t.Fatalf("expected delegation guidance in system prompt: %q", with)
	}
	// Default (no specialists) must reproduce the prior prompt: no delegation block.
	without := buildSystemPrompt(Options{})
	if strings.Contains(without, "<specialists>") {
		t.Fatalf("delegation block must not appear without specialists")
	}
}
