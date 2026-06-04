package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const version = "0.1.0"

// Run executes the minimal Go CLI surface. It returns an exit code so tests can
// exercise command behavior without terminating the test process.
func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		if err := writeHelp(stdout); err != nil {
			return 1
		}
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		if err := writeHelp(stdout); err != nil {
			return 1
		}
		return 0
	case "-v", "--version", "version":
		if _, err := fmt.Fprintf(stdout, "zero %s\n", version); err != nil {
			return 1
		}
		return 0
	case "exec":
		return runExec(args[1:], stdout, stderr)
	default:
		if _, err := fmt.Fprintf(stderr, "unknown command %q\n", args[0]); err != nil {
			return 1
		}
		if _, err := fmt.Fprintln(stderr, "Run zero --help for usage."); err != nil {
			return 1
		}
		return 2
	}
}

func writeHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `ZERO terminal coding agent

Usage:
  zero [command]

Commands:
  exec       Run a one-shot prompt through the Go agent runtime
  help       Show this help
  version    Print version

Flags:
  -h, --help       Show this help
  -v, --version    Print version
`)
	return err
}

func runExec(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		return writePromptRequired(stderr)
	}

	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		if err := writeExecHelp(stdout); err != nil {
			return 1
		}
		return 0
	}

	prompt := strings.TrimSpace(strings.Join(args, " "))
	if prompt == "" {
		return writePromptRequired(stderr)
	}

	workspaceRoot, err := os.Getwd()
	if err != nil {
		if _, writeErr := fmt.Fprintf(stderr, "failed to resolve workspace: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}

	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workspaceRoot) {
		registry.Register(tool)
	}

	result, err := agent.Run(context.Background(), prompt, offlineProvider{}, agent.Options{
		Registry: registry,
	})
	if err != nil {
		if _, writeErr := fmt.Fprintf(stderr, "agent runtime failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}

	if _, err := fmt.Fprintln(stdout, result.FinalAnswer); err != nil {
		return 1
	}
	return 0
}

func writePromptRequired(stderr io.Writer) int {
	if _, err := fmt.Fprintln(stderr, "Prompt required. Use `zero exec \"prompt\"`."); err != nil {
		return 1
	}
	return 2
}

func writeExecHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero exec <prompt>

Runs a one-shot prompt through the Go agent runtime.
`)
	return err
}

type offlineProvider struct{}

func (offlineProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	prompt := ""
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == zeroruntime.MessageRoleUser {
			prompt = request.Messages[index].Content
			break
		}
	}

	ch := make(chan zeroruntime.StreamEvent, 2)
	select {
	case <-ctx.Done():
		close(ch)
		return ch, ctx.Err()
	case ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: "Go agent runtime ready: " + prompt}:
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}
