package hooks

import (
	"strings"
	"testing"
)

// Same contract on the hook path. The projection kept stdout, stderr and an exit
// code and dropped the enforcement notices, so a hook ran under the weakened
// token silently.
func TestAHookSurfacesTheEnforcementNotice(t *testing.T) {
	const notice = "denyRead is configured, so the write jail is not confining writes"

	for _, testCase := range []struct {
		name   string
		result commandResult
		want   string
	}{
		{"hook printed nothing", commandResult{ExitCode: 0, Notices: []string{notice}}, notice},
		{"hook printed to stdout", commandResult{ExitCode: 0, Stdout: "looks fine", Notices: []string{notice}}, notice},
		{"hook printed to stderr only", commandResult{ExitCode: 0, Stderr: "a warning", Notices: []string{notice}}, notice},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			message := hookMessage(testCase.result)
			if !strings.Contains(message, testCase.want) {
				t.Fatalf("the hook message does not carry the notice:\n%s", message)
			}
			if strings.Count(message, testCase.want) != 1 {
				t.Errorf("the notice appears %d times, want exactly once:\n%s", strings.Count(message, testCase.want), message)
			}
		})
	}
}

// A hook with no notice reads exactly as it did before.
func TestAHookWithoutANoticeIsUnchanged(t *testing.T) {
	if message := hookMessage(commandResult{ExitCode: 0, Stdout: "looks fine"}); message != "looks fine" {
		t.Errorf("hookMessage = %q, want the hook's own output untouched", message)
	}
	if message := hookMessage(commandResult{ExitCode: 0}); message != "" {
		t.Errorf("a silent hook with no notice produced %q", message)
	}
}
