package specialist

import (
	"strings"
	"testing"
)

// argValue returns the value following flag in argv, and whether it was present.
func argValue(args []string, flag string) (string, bool) {
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			return args[index+1], true
		}
	}
	return "", false
}

func pinnedManifest(model string, effort string) Manifest {
	return Manifest{
		Metadata: Metadata{
			Name:            "skim",
			Description:     "Bounded read-only lookups.",
			Model:           model,
			ReasoningEffort: effort,
			Tools:           []string{"read_file"},
		},
		SystemPrompt:  "Find the thing and stop.",
		ResolvedTools: []string{"read_file"},
	}
}

// A RESUMED SPECIALIST KEEPS THE MODEL ITS MANIFEST PINNED.
//
// Metadata.Model exists so a bounded, delegated task can run on a cheaper model
// than its parent. BuildArgs appended it; BuildResumeArgs did not. So the
// specialist ran on the cheap model once, and the moment the orchestrator
// resumed it the child fell back to whatever the parent's configured model
// resolved to. Nothing surfaced it: the resumed child starts normally, does the
// work, and the only symptom is the bill.
//
// Resuming does not restore the recorded model on its own, which is why the flag
// has to be passed again rather than relied upon.
func TestAResumedSpecialistKeepsItsPinnedModel(t *testing.T) {
	manifest := pinnedManifest("claude-haiku-4.5", "")

	fresh, err := (Executor{}).BuildArgs(BuildArgsInput{
		Manifest:     manifest,
		Prompt:       "find the thing",
		CurrentDepth: 0,
		ParentModel:  "claude-opus-4.1",
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	// SETUP: the fresh path really does pin it, or the comparison below is
	// asserting against a path that never worked either.
	freshModel, ok := argValue(fresh.Args, "--model")
	if !ok || freshModel != "claude-haiku-4.5" {
		t.Fatalf("SETUP INVALID: the fresh path passed --model %q (present=%t), want the manifest's model", freshModel, ok)
	}

	resumed, err := (Executor{}).BuildResumeArgs(BuildResumeArgsInput{
		SessionID:    "01HZZZZZZZZZZZZZZZZZZZZZZZ",
		Prompt:       "keep going",
		CurrentDepth: 0,
		Manifest:     manifest,
		ParentModel:  "claude-opus-4.1",
	})
	if err != nil {
		t.Fatalf("BuildResumeArgs: %v", err)
	}
	resumedModel, ok := argValue(resumed.Args, "--model")
	if !ok {
		t.Fatalf("a resumed specialist carries no --model at all, so it reverts to the parent's configured model: %v", resumed.Args)
	}
	if resumedModel != "claude-haiku-4.5" {
		t.Fatalf("a resumed specialist runs on %q, want the manifest's pinned %q", resumedModel, "claude-haiku-4.5")
	}
}

// And a manifest that pins nothing still inherits the parent's model, which is
// what makes the fallback in appendModelArgs meaningful rather than the pinned
// case being special-cased.
func TestAResumedSpecialistWithNoPinInheritsTheParentModel(t *testing.T) {
	resumed, err := (Executor{}).BuildResumeArgs(BuildResumeArgsInput{
		SessionID:    "01HZZZZZZZZZZZZZZZZZZZZZZZ",
		Prompt:       "keep going",
		CurrentDepth: 0,
		Manifest:     pinnedManifest("", ""),
		ParentModel:  "claude-opus-4.1",
	})
	if err != nil {
		t.Fatalf("BuildResumeArgs: %v", err)
	}
	model, ok := argValue(resumed.Args, "--model")
	if !ok || model != "claude-opus-4.1" {
		t.Fatalf("a resumed specialist with no pinned model passed --model %q (present=%t), want the parent's", model, ok)
	}
}

// The reasoning-effort rule travels with the model, or the two paths disagree
// about what a pinned model implies. appendModelArgs inherits the parent's
// effort ONLY when the manifest pins no model of its own: a manifest that chose
// a different model has not agreed to the parent's effort for it.
func TestAResumedSpecialistFollowsTheSameReasoningEffortRule(t *testing.T) {
	t.Run("pinned model does not inherit parent effort", func(t *testing.T) {
		resumed, err := (Executor{}).BuildResumeArgs(BuildResumeArgsInput{
			SessionID:             "01HZZZZZZZZZZZZZZZZZZZZZZZ",
			Prompt:                "keep going",
			CurrentDepth:          0,
			Manifest:              pinnedManifest("claude-haiku-4.5", ""),
			ParentModel:           "claude-opus-4.1",
			ParentReasoningEffort: "high",
		})
		if err != nil {
			t.Fatalf("BuildResumeArgs: %v", err)
		}
		if effort, ok := argValue(resumed.Args, "--reasoning-effort"); ok {
			t.Fatalf("a manifest that pinned its own model inherited the parent's effort %q", effort)
		}
	})

	t.Run("no pinned model inherits parent effort", func(t *testing.T) {
		resumed, err := (Executor{}).BuildResumeArgs(BuildResumeArgsInput{
			SessionID:             "01HZZZZZZZZZZZZZZZZZZZZZZZ",
			Prompt:                "keep going",
			CurrentDepth:          0,
			Manifest:              pinnedManifest("", ""),
			ParentModel:           "claude-opus-4.1",
			ParentReasoningEffort: "high",
		})
		if err != nil {
			t.Fatalf("BuildResumeArgs: %v", err)
		}
		effort, ok := argValue(resumed.Args, "--reasoning-effort")
		if !ok || effort != "high" {
			t.Fatalf("a manifest pinning nothing passed --reasoning-effort %q (present=%t), want the parent's", effort, ok)
		}
	})
}

// The two builders agree on where the flag sits relative to the rest of the
// argv, so a future change to one does not silently reorder only the other.
func TestBothBuildersPlaceTheModelBeforeTheAutonomyFlag(t *testing.T) {
	manifest := pinnedManifest("claude-haiku-4.5", "")
	fresh, err := (Executor{}).BuildArgs(BuildArgsInput{Manifest: manifest, Prompt: "go", CurrentDepth: 0})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	resumed, err := (Executor{}).BuildResumeArgs(BuildResumeArgsInput{
		SessionID: "01HZZZZZZZZZZZZZZZZZZZZZZZ", Prompt: "go", CurrentDepth: 0, Manifest: manifest,
	})
	if err != nil {
		t.Fatalf("BuildResumeArgs: %v", err)
	}
	for _, argv := range [][]string{fresh.Args, resumed.Args} {
		model := indexOf(argv, "--model")
		auto := indexOf(argv, "--auto")
		if model < 0 || auto < 0 || model > auto {
			t.Fatalf("--model at %d, --auto at %d in %s", model, auto, strings.Join(argv, " "))
		}
	}
}

func indexOf(args []string, want string) int {
	for index, arg := range args {
		if arg == want {
			return index
		}
	}
	return -1
}
