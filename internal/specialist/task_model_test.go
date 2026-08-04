package specialist

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/streamjson"
)

// A TASK CAN NAME ITS OWN MODEL, so a standalone spawn need not inherit the
// parent's — the missing half of per-sub-agent model routing outside a plan.
//
// Asserted on ARGV, not the struct: a Model field that never reaches --model is
// a field that does nothing, which is exactly the "layer B doesn't carry it"
// defect this repo keeps finding.
func TestATaskModelReachesTheChildsArgv(t *testing.T) {
	var childArgs []string
	executor := Executor{
		BinaryPath:   "/usr/local/bin/zero",
		NewSessionID: func() (string, error) { return "specialist_00000000000000000000000a", nil },
		Load: func(LoadOptions) (LoadResult, error) {
			return LoadResult{Specialists: []Manifest{{
				Metadata: Metadata{Name: "worker", Description: "does work"}, SystemPrompt: "work.", ResolvedTools: []string{"read_file"},
			}}}, nil
		},
		// Captures the argv the real runFresh path built, so this exercises the
		// call site — not applyTaskModel in isolation.
		RunChild: func(_ context.Context, _ string, args []string, progress func(streamjson.Event)) (ChildRunResult, error) {
			childArgs = append([]string(nil), args...)
			events := []streamjson.Event{
				{Type: streamjson.EventRunStart, SessionID: "specialist_00000000000000000000000a"},
				{Type: streamjson.EventFinal, Text: "done"},
				{Type: streamjson.EventRunEnd, Status: "success"},
			}
			for _, e := range events {
				if progress != nil {
					progress(e)
				}
			}
			return ChildRunResult{Started: true, ExitCode: 0, Events: events}, nil
		},
	}

	_, err := executor.Run(context.Background(), TaskParameters{Name: "worker", Prompt: "audit it", Model: "kimi-k2.6"},
		TaskRunOptions{Cwd: t.TempDir(), ParentReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(childArgs, " ")
	if !strings.Contains(joined, "--model kimi-k2.6") {
		t.Fatalf("the Task model never reached the child argv via the real Run path:\n%s", joined)
	}
}

// NAMING A MODEL MUST NOT DROP THE POSTURE'S EFFORT. appendModelArgs forwards
// the parent's effort only when NO model is named, so a Task naming a model
// would otherwise lose the raised zeromaxing effort. applyTaskModel forwards it.
func TestATaskModelStillForwardsTheParentEffort(t *testing.T) {
	var manifest Manifest
	manifest.Metadata.Name = "worker"
	if err := applyTaskModel(&manifest, "kimi-k2.6", "high"); err != nil {
		t.Fatal(err)
	}
	if manifest.Metadata.Model != "kimi-k2.6" {
		t.Fatalf("model = %q, want kimi-k2.6", manifest.Metadata.Model)
	}
	if manifest.Metadata.ReasoningEffort != "high" {
		t.Fatalf("naming a model dropped the parent effort: got %q, want high", manifest.Metadata.ReasoningEffort)
	}

	// A manifest that names its OWN effort keeps it.
	own := Manifest{}
	own.Metadata.Name = "judge"
	own.Metadata.ReasoningEffort = "max"
	if err := applyTaskModel(&own, "glm-5.2", "high"); err != nil {
		t.Fatal(err)
	}
	if own.Metadata.ReasoningEffort != "max" {
		t.Fatalf("a manifest's own effort was overwritten: %q", own.Metadata.ReasoningEffort)
	}
}

// AN EMPTY MODEL CHANGES NOTHING — every existing spawn stays on the inherited
// model, byte-for-byte.
func TestAnEmptyTaskModelIsAPureNoOp(t *testing.T) {
	manifest := Manifest{}
	manifest.Metadata.Name = "worker"
	before := manifest
	if err := applyTaskModel(&manifest, "  ", "high"); err != nil {
		t.Fatal(err)
	}
	if manifest.Metadata.Model != "" || manifest.Metadata.ReasoningEffort != before.Metadata.ReasoningEffort {
		t.Fatalf("an empty model was not a no-op: %+v", manifest.Metadata)
	}
}

// The parser reads the model argument.
func TestTheTaskToolParsesTheModelArgument(t *testing.T) {
	params, err := parseTaskParameters(map[string]any{"name": "worker", "prompt": "x", "model": "deepseek-v4-flash"})
	if err != nil {
		t.Fatal(err)
	}
	if params.Model != "deepseek-v4-flash" {
		t.Fatalf("parsed model = %q", params.Model)
	}
}
