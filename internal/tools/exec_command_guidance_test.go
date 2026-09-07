package tools

import (
	"runtime"
	"strings"
	"testing"
)

// THE BUDGET TEST SUBTRACTS THIS, SO IT HAS TO BE EXACTLY WHAT IS ADDED.
//
// internal/agent's eager-schema ratchet charges the tool schemas and this host's
// shell guidance separately, and it separates them by subtracting
// HostExecCommandShellGuidance from the measured total. That arithmetic is only
// valid while the guidance is appended verbatim to exec_command's description.
// If the two drift apart the ratchet keeps reporting a number, just not the one
// it claims, so this pins the relationship rather than leaving it to a comment.
func TestHostExecCommandShellGuidanceMatchesTheDescription(t *testing.T) {
	tool := NewExecCommandTool(t.TempDir(), nil)
	description := tool.Description()
	guidance := HostExecCommandShellGuidance()

	if runtime.GOOS != "windows" {
		if guidance != "" {
			t.Fatalf("no shell guidance is appended off Windows, but HostExecCommandShellGuidance returned %q", guidance)
		}
		return
	}
	if guidance == "" {
		t.Fatal("exec_command appends shell guidance on Windows, but HostExecCommandShellGuidance returned nothing; the budget would charge it to the schemas")
	}
	if !strings.HasSuffix(description, guidance) {
		t.Fatalf("the guidance is not the tail of exec_command's description, so subtracting it from the schema total is wrong.\nguidance: %q\ndescription tail: %q",
			guidance, description[max(0, len(description)-len(guidance)-40):])
	}
}

// And the guidance a Windows host gets is never empty, whichever shell was
// detected, since an empty one would mean the model gets no syntax rule at all.
func TestWindowsShellGuidanceIsNeverEmpty(t *testing.T) {
	for _, shell := range []shellRuntime{
		{GOOS: "windows", Executable: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, Kind: shellKindPowerShell, Syntax: "PowerShell"},
		{GOOS: "windows", Executable: `C:\Program Files\PowerShell\7\pwsh.exe`, Kind: shellKindPowerShell, Syntax: "PowerShell"},
		{GOOS: "windows", Executable: `C:\Windows\System32\cmd.exe`, Kind: shellKindCmd, Syntax: "cmd.exe"},
	} {
		if guidance := shellGuidanceForRuntime(shell); strings.TrimSpace(guidance) == "" {
			t.Errorf("%s produced no shell guidance", shell.Executable)
		}
	}
}
