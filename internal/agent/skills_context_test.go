package agent

import (
	"strconv"
	"strings"
	"testing"
)

func TestSkillsContextCapsLongList(t *testing.T) {
	skills := make([]SkillInfo, 0, 40)
	for i := range 40 {
		n := strconv.Itoa(i)
		skills = append(skills, SkillInfo{Name: "skill-" + n, Description: "does something useful, number " + n})
	}
	got := skillsContext(Options{Skills: skills})
	if len(got) > 1200 {
		t.Fatalf("skills block should stay bounded, got %d bytes:\n%s", len(got), got)
	}
	if !strings.Contains(got, "more (call skill") {
		t.Fatalf("expected an overflow summary line, got:\n%s", got)
	}
	// The first skill is always listed regardless of budget.
	if !strings.Contains(got, "- skill-0:") {
		t.Fatalf("expected the first skill to always be listed, got:\n%s", got)
	}
}

func TestSkillsContext(t *testing.T) {
	if got := skillsContext(Options{}); got != "" {
		t.Fatalf("no skills should yield an empty section, got %q", got)
	}
	got := skillsContext(Options{Skills: []SkillInfo{
		{Name: "commit-writer", Description: "Write a conventional-commit message."},
		{Name: "  ", Description: "nameless, should be skipped"},
		{Name: "reviewer"},
	}})
	if !strings.Contains(got, "<available_skills>") || !strings.Contains(got, "</available_skills>") {
		t.Fatalf("missing available_skills block: %q", got)
	}
	if !strings.Contains(got, "- commit-writer: Write a conventional-commit message.") {
		t.Fatalf("missing commit-writer line: %q", got)
	}
	if !strings.Contains(got, "- reviewer\n") {
		t.Fatalf("reviewer (no description) line missing: %q", got)
	}
	if strings.Contains(got, "nameless") {
		t.Fatalf("nameless entry should be skipped: %q", got)
	}
}

func TestSystemPromptIncludesSkillsOnlyWhenInstalled(t *testing.T) {
	with := buildSystemPrompt(Options{Skills: []SkillInfo{
		{Name: "commit-writer", Description: "Write a commit message."},
	}})
	if !strings.Contains(with, "<available_skills>") || !strings.Contains(with, "skill tool") {
		t.Fatalf("expected available_skills guidance in system prompt: %q", with)
	}
	// Default (no skills) must reproduce the prior prompt: no skills block.
	without := buildSystemPrompt(Options{})
	if strings.Contains(without, "<available_skills>") {
		t.Fatalf("available_skills block must not appear without skills")
	}
}
