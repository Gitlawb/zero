package plugins

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/execution"
	"github.com/Gitlawb/zero/internal/tools"
)

// THE DISCLOSURE HAS TO SURVIVE THE PROJECTION.
//
// The execution runner puts enforcement notices on the structured outcome, and
// this path used to copy only stdout, stderr and an exit code out of it. A
// plugin tool therefore ran under the non-WRITE_RESTRICTED token and returned a
// result that said nothing about the write jail it had just traded away.
//
// Asserted through pluginTool.invoke, which is what the registry calls, and
// through Result.ModelOutput, which is what the model actually reads.
func TestAPluginToolCarriesTheEnforcementNotice(t *testing.T) {
	const notice = "denyRead is configured, so the write jail is not confining writes"

	for _, testCase := range []struct {
		name     string
		exitCode int
	}{
		{"successful command", 0},
		{"failed command", 3},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tool := pluginTool{
				name: "demo",
				run: func(context.Context, pluginCommand) commandOutput {
					return commandOutput{Stdout: "hello", ExitCode: testCase.exitCode, Notices: []string{notice}}
				},
			}

			result := tool.invoke(context.Background(), map[string]any{}, t.TempDir())
			if len(result.EnforcementNotices) == 0 {
				t.Fatal("the plugin result carried no enforcement notice; the command ran under the weakened token and said nothing")
			}
			model := result.ModelOutput()
			if !strings.Contains(model, notice) {
				t.Errorf("the model-facing output does not contain the notice:\n%s", model)
			}
			if strings.Count(model, notice) != 1 {
				t.Errorf("the notice appears %d times, want exactly once:\n%s", strings.Count(model, notice), model)
			}
			if summary := result.HumanDisplay().Summary; !strings.Contains(summary, notice) {
				t.Errorf("the human summary does not contain the notice: %q", summary)
			}
		})
	}
}

// And a command with no notice is unchanged, or the assertion above would be
// satisfied by text pasted onto everything.
func TestAPluginToolWithoutANoticeIsUnchanged(t *testing.T) {
	tool := pluginTool{
		name: "demo",
		run: func(context.Context, pluginCommand) commandOutput {
			return commandOutput{Stdout: "hello", ExitCode: 0}
		},
	}
	result := tool.invoke(context.Background(), map[string]any{}, t.TempDir())
	if len(result.EnforcementNotices) != 0 {
		t.Errorf("a command with no enforcement notice grew one: %v", result.EnforcementNotices)
	}
	if result.Status != tools.StatusOK {
		t.Errorf("status = %v, want ok", result.Status)
	}
}

// A TIMEOUT OR CANCELLATION STILL RAN THE CHILD.
//
// invoke's error branch rebuilt a result from status, output and metadata alone,
// so a plugin that timed out under the non-WRITE_RESTRICTED token reported only
// the timeout. The process had already launched without write confinement;
// whether the disclosure survives must not depend on how it ended.
func TestAPluginToolCarriesTheNoticeWhenItTimesOutOrIsCancelled(t *testing.T) {
	const notice = "denyRead is configured, so the write jail is not confining writes"

	for _, testCase := range []struct {
		name string
		err  error
	}{
		{"timed out", context.DeadlineExceeded},
		{"cancelled", context.Canceled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tool := pluginTool{
				name: "demo",
				run: func(context.Context, pluginCommand) commandOutput {
					return commandOutput{ExitCode: -1, Err: testCase.err, Notices: []string{notice}}
				},
			}
			result := tool.invoke(context.Background(), map[string]any{}, t.TempDir())

			model := result.ModelOutput()
			if !strings.Contains(model, notice) {
				t.Errorf("the model-facing output lost the notice:\n%s", model)
			}
			if strings.Count(model, notice) != 1 {
				t.Errorf("the notice appears %d times, want once:\n%s", strings.Count(model, notice), model)
			}
			if summary := result.HumanDisplay().Summary; !strings.Contains(summary, notice) {
				t.Errorf("the human summary lost the notice: %q", summary)
			}
		})
	}
}

// But a child that never launched must stay silent, or the notice describes a
// trade nobody made. This is the distinction the launched-or-not check exists
// for, and without it the assertion above would be satisfied by pasting the
// notice onto every error.
func TestAPluginThatNeverLaunchedCarriesNoNotice(t *testing.T) {
	for _, kind := range []execution.OutcomeKind{
		execution.OutcomeSandboxSetupFailure,
		execution.OutcomeExecutableNotFound,
	} {
		if pluginChildLaunched(kind) {
			t.Errorf("%v is treated as a launched child; a notice there would describe a process that never started", kind)
		}
	}
	for _, kind := range []execution.OutcomeKind{
		execution.OutcomeTimedOut,
		execution.OutcomeCancelled,
	} {
		if !pluginChildLaunched(kind) {
			t.Errorf("%v is treated as never launched, so its disclosure would be dropped", kind)
		}
	}
}
