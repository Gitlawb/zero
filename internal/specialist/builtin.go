package specialist

import "fmt"

func Builtins() []Manifest {
	builtins := []Manifest{
		{
			Metadata: Metadata{
				Name:        "worker",
				Description: "Handles general delegated coding tasks and reports concrete outcomes.",
				Tools:       []string{"read-only", "edit", "execute", "plan"},
			},
			SystemPrompt: workerPrompt,
			Location:     LocationBuiltin,
			FilePath:     "(builtin)",
		},
		{
			Metadata: Metadata{
				Name:        "explorer",
				Description: "Performs fast read-only codebase exploration without modifying files.",
				Tools:       []string{"read-only"},
			},
			SystemPrompt: explorerPrompt,
			Location:     LocationBuiltin,
			FilePath:     "(builtin)",
		},
		{
			Metadata: Metadata{
				Name:        "code-review",
				Description: "Reviews code changes for correctness, regressions, and missing tests.",
				Tools:       []string{"read-only"},
			},
			SystemPrompt: codeReviewPrompt,
			Location:     LocationBuiltin,
			FilePath:     "(builtin)",
		},
	}
	for index := range builtins {
		if err := Validate(&builtins[index]); err != nil {
			panic(fmt.Sprintf("invalid built-in specialist %q: %s", builtins[index].Metadata.Name, err))
		}
	}
	return builtins
}

const workerPrompt = `You are a focused task specialist inside Zero.

Complete the assigned task precisely, stay within scope, and report:
- the concrete work performed
- the outcome
- any blockers or follow-ups`

const explorerPrompt = `You are a read-only codebase exploration specialist inside Zero.

Find relevant files, symbols, tests, and behavior quickly. Do not edit files or run shell commands. Report concise findings with paths and line references when useful.`

const codeReviewPrompt = `You are a code review specialist inside Zero.

Review changes for correctness bugs, regressions, unsafe behavior, and missing tests. Prioritize actionable findings over style feedback.`
