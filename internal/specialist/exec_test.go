package specialist

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildArgsCreatesFreshSpecialistExecInvocation(t *testing.T) {
	executor := Executor{
		NewSessionID: func() (string, error) { return "child_session", nil },
	}
	manifest := Manifest{
		Metadata: Metadata{
			Name:            "reviewer",
			Description:     "Reviews code",
			Model:           "claude-sonnet-4.5",
			ReasoningEffort: "high",
		},
		SystemPrompt:  "Review carefully.",
		ResolvedTools: []string{"grep", "read_file"},
	}

	result, err := executor.BuildArgs(BuildArgsInput{
		Manifest:              manifest,
		Prompt:                "Review this patch",
		ParentSessionID:       "parent_session",
		ParentToolUseID:       "toolu_123",
		ParentModel:           "gpt-4.1",
		ParentReasoningEffort: "medium",
		CurrentDepth:          1,
		Description:           "Auth diff",
	})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}
	if result.SessionID != "child_session" || result.PromptFile != "" {
		t.Fatalf("unexpected result metadata: %#v", result)
	}
	wantArgs := []string{
		"exec", "--init-session-id", "child_session",
		result.Args[3],
		"--model", "claude-sonnet-4.5",
		"--reasoning-effort", "high",
		"--auto", "high",
		"--output-format", "stream-json",
		"--enabled-tools", "grep,read_file",
		"--depth", "2",
		"--tag", "specialist",
		"--calling-session-id", "parent_session",
		"--calling-tool-use-id", "toolu_123",
		"--session-title", "reviewer: Auth diff",
	}
	if !reflect.DeepEqual(result.Args, wantArgs) {
		t.Fatalf("args mismatch\ngot:  %#v\nwant: %#v", result.Args, wantArgs)
	}
	prompt := result.Args[3]
	for _, want := range []string{"Specialist: reviewer", "Task description: Auth diff", "Review carefully.", "Review this patch"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("wrapped prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSpecialistAutonomyByPermissionMode(t *testing.T) {
	cases := map[string]string{
		"":           "high", // back-compat: the Task tool omits the mode
		"unsafe":     "high",
		"auto":       "low",
		"ask":        "low",
		"spec-draft": "low",
		"whatever":   "low",
	}
	for mode, want := range cases {
		if got := specialistAutonomy(mode); got != want {
			t.Errorf("specialistAutonomy(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestBuildArgsAutonomyHonorsPermissionMode(t *testing.T) {
	executor := Executor{NewSessionID: func() (string, error) { return "child", nil }}
	manifest := Manifest{Metadata: Metadata{Name: "reviewer"}, SystemPrompt: "x", ResolvedTools: []string{"read_file"}}

	// A non-unsafe parent mode yields a non-unsafe child (--auto low), so a
	// swarm member never gains more authority than a non-unsafe orchestrator.
	res, err := executor.BuildArgs(BuildArgsInput{Manifest: manifest, Prompt: "p", PermissionMode: "auto"})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	if !containsSequence(res.Args, []string{"--auto", "low"}) {
		t.Fatalf("non-unsafe parent must yield --auto low, got %v", res.Args)
	}
	if containsSequence(res.Args, []string{"--auto", "high"}) {
		t.Fatalf("non-unsafe parent must NOT yield --auto high: %v", res.Args)
	}

	// An unsafe parent (and the empty back-compat case) keep --auto high.
	for _, mode := range []string{"unsafe", ""} {
		out, err := executor.BuildArgs(BuildArgsInput{Manifest: manifest, Prompt: "p", PermissionMode: mode})
		if err != nil {
			t.Fatalf("BuildArgs(%q): %v", mode, err)
		}
		if !containsSequence(out.Args, []string{"--auto", "high"}) {
			t.Fatalf("mode %q must yield --auto high, got %v", mode, out.Args)
		}
	}
}

func TestBuildResumeArgsAutonomyHonorsPermissionMode(t *testing.T) {
	executor := Executor{}
	manifest := Manifest{Metadata: Metadata{Name: "reviewer"}, ResolvedTools: []string{"read_file"}}
	res, err := executor.BuildResumeArgs(BuildResumeArgsInput{
		SessionID: "child_session", Prompt: "p", Manifest: manifest, PermissionMode: "auto",
	})
	if err != nil {
		t.Fatalf("BuildResumeArgs: %v", err)
	}
	if !containsSequence(res.Args, []string{"--auto", "low"}) || containsSequence(res.Args, []string{"--auto", "high"}) {
		t.Fatalf("resume non-unsafe parent must yield --auto low, got %v", res.Args)
	}
}

func TestBuildArgsSessionTitleFallsBackToNameWithoutDescription(t *testing.T) {
	executor := Executor{NewSessionID: func() (string, error) { return "child", nil }}
	manifest := Manifest{
		Metadata:      Metadata{Name: "reviewer", Description: "Reviews code"},
		SystemPrompt:  "Review carefully.",
		ResolvedTools: []string{"read_file"},
	}

	// Description is optional. When omitted, the session title must still carry
	// the specialist name so AgentName (derived from the title) is non-empty and
	// the session remains resumable.
	result, err := executor.BuildArgs(BuildArgsInput{
		Manifest: manifest,
		Prompt:   "Do the thing",
	})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}
	if !containsSequence(result.Args, []string{"--session-title", "reviewer"}) {
		t.Fatalf("args missing name-only session title: %#v", result.Args)
	}
	for index, arg := range result.Args {
		if arg == "--session-title" && index+1 < len(result.Args) {
			if got := result.Args[index+1]; got != "reviewer" {
				t.Fatalf("session title = %q, want %q", got, "reviewer")
			}
		}
	}
}

func TestBuildArgsInheritsParentModelAndReasoning(t *testing.T) {
	executor := Executor{NewSessionID: func() (string, error) { return "child", nil }}
	manifest := Manifest{
		Metadata:      Metadata{Name: "worker", Description: "Works"},
		SystemPrompt:  "Do work.",
		ResolvedTools: []string{"read_file"},
	}

	result, err := executor.BuildArgs(BuildArgsInput{
		Manifest:              manifest,
		Prompt:                "Do the thing",
		ParentModel:           "gpt-4.1",
		ParentReasoningEffort: "medium",
	})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}
	if !containsSequence(result.Args, []string{"--model", "gpt-4.1"}) {
		t.Fatalf("args missing inherited model: %#v", result.Args)
	}
	if !containsSequence(result.Args, []string{"--reasoning-effort", "medium"}) {
		t.Fatalf("args missing inherited reasoning effort: %#v", result.Args)
	}
}

func TestBuildArgsDefaultsToReadOnlyToolAllowlist(t *testing.T) {
	executor := Executor{NewSessionID: func() (string, error) { return "child", nil }}

	result, err := executor.BuildArgs(BuildArgsInput{
		Manifest: Manifest{
			Metadata:     Metadata{Name: "worker", Description: "Works"},
			SystemPrompt: "Do work.",
		},
		Prompt: "Do the thing",
	})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}
	if !containsSequence(result.Args, []string{"--enabled-tools", "glob,grep,list_directory,read_file"}) {
		t.Fatalf("args missing default read-only allowlist: %#v", result.Args)
	}
}

func TestBuildArgsWritesLargePromptFile(t *testing.T) {
	root := t.TempDir()
	var writtenPrompt string
	executor := Executor{
		NewSessionID:      func() (string, error) { return "child", nil },
		PromptFileMaxSize: 16,
		WritePromptFile: func(prompt string) (string, error) {
			writtenPrompt = prompt
			path := filepath.Join(root, "prompt.md")
			return path, os.WriteFile(path, []byte(prompt), 0o600)
		},
	}

	result, err := executor.BuildArgs(BuildArgsInput{
		Manifest: Manifest{
			Metadata:     Metadata{Name: "worker", Description: "Works"},
			SystemPrompt: strings.Repeat("system ", 10),
		},
		Prompt: "Do the large thing",
	})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}
	if result.PromptFile != filepath.Join(root, "prompt.md") {
		t.Fatalf("PromptFile = %q", result.PromptFile)
	}
	if !reflect.DeepEqual(result.Args[:5], []string{"exec", "--init-session-id", "child", "--file", result.PromptFile}) {
		t.Fatalf("prompt file args = %#v", result.Args[:5])
	}
	if !strings.Contains(writtenPrompt, "Do the large thing") {
		t.Fatalf("written prompt missing task: %s", writtenPrompt)
	}
}

func TestBuildResumeArgsUsesExistingSession(t *testing.T) {
	result, err := (Executor{}).BuildResumeArgs(BuildResumeArgsInput{
		SessionID:    "child_session",
		Prompt:       "Follow up",
		CurrentDepth: 2,
	})
	if err != nil {
		t.Fatalf("BuildResumeArgs returned error: %v", err)
	}
	wantPrefix := []string{"exec", "--resume", "child_session"}
	if !reflect.DeepEqual(result.Args[:3], wantPrefix) {
		t.Fatalf("resume args prefix = %#v", result.Args[:3])
	}
	if !containsSequence(result.Args, []string{"--auto", "high"}) ||
		!containsSequence(result.Args, []string{"--output-format", "stream-json"}) ||
		!containsSequence(result.Args, []string{"--depth", "3"}) ||
		!containsSequence(result.Args, []string{"--tag", "specialist"}) {
		t.Fatalf("resume args missing required flags: %#v", result.Args)
	}
	if !strings.Contains(result.Args[3], "Follow-up Instructions") || !strings.Contains(result.Args[3], "Follow up") {
		t.Fatalf("resume prompt not wrapped correctly: %s", result.Args[3])
	}
}

func TestBuildArgsRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input BuildArgsInput
		want  string
	}{
		{name: "negative depth", input: BuildArgsInput{Prompt: "hi", CurrentDepth: -1}, want: "depth"},
		{name: "empty prompt", input: BuildArgsInput{CurrentDepth: 0}, want: "prompt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (Executor{}).BuildArgs(tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BuildArgs error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildArgsRejectsInvalidSessionIDs(t *testing.T) {
	_, err := (Executor{NewSessionID: func() (string, error) { return "../escape", nil }}).BuildArgs(BuildArgsInput{
		Prompt: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid specialist session id") {
		t.Fatalf("BuildArgs error = %v", err)
	}

	_, err = (Executor{}).BuildResumeArgs(BuildResumeArgsInput{
		SessionID: "../escape",
		Prompt:    "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid resume session id") {
		t.Fatalf("BuildResumeArgs error = %v", err)
	}
}

func TestBuildArgsNormalizesGeneratedSessionID(t *testing.T) {
	result, err := (Executor{
		NewSessionID: func() (string, error) { return " child_session ", nil },
	}).BuildArgs(BuildArgsInput{Prompt: "hi"})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}
	if result.SessionID != "child_session" {
		t.Fatalf("SessionID = %q, want child_session", result.SessionID)
	}
	if !containsSequence(result.Args, []string{"--init-session-id", "child_session"}) {
		t.Fatalf("args missing normalized session id: %#v", result.Args)
	}
}

func containsSequence(values []string, sequence []string) bool {
	for index := 0; index+len(sequence) <= len(values); index++ {
		if reflect.DeepEqual(values[index:index+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
