package plugins

import (
	"context"
	"strings"
	"testing"

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
