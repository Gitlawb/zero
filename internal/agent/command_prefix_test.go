package agent

import "testing"

func TestProposedCommandPrefixUsesSafeSimpleCommands(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{"command": "go test ./..."})
	want := []string{"go", "test", "./..."}
	if !equalStringSlices(got, want) {
		t.Fatalf("prefix = %#v, want %#v", got, want)
	}
}

func TestProposedCommandPrefixHonorsValidatedRequestedPrefix(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{
		"command":     "git status --short",
		"prefix_rule": []any{"git", "status"},
	})
	want := []string{"git", "status"}
	if !equalStringSlices(got, want) {
		t.Fatalf("prefix = %#v, want %#v", got, want)
	}
}

func TestProposedCommandPrefixRejectsUnsafeRequestedPrefix(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{
		"command":     "git status --short",
		"prefix_rule": []any{"git"},
	})
	if got != nil {
		t.Fatalf("broad requested prefix should be rejected, got %#v", got)
	}
}

func TestProposedCommandPrefixRejectsUnsafeShellForms(t *testing.T) {
	cases := []string{
		"echo hi && echo bye",
		"cat < in > out",
		"FOO=bar go test",
		"echo $(whoami)",
		"cat *.go",
		"bash -lc go test",
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if got := proposedCommandPrefix("bash", map[string]any{"command": command}); got != nil {
				t.Fatalf("unsafe command got prefix %#v", got)
			}
		})
	}
}

func TestProposedCommandPrefixRejectsUnsafeLaunchers(t *testing.T) {
	cases := []string{
		"find . -type f",
		"xargs rm -rf /tmp/x",
		"timeout 5 go test ./...",
		"nice go test ./...",
		"nohup go test ./...",
		"watch ls",
		"ssh host ls",
		"make test",
		"npm run dev",
		"command git status",
		"eval echo hi",
		"exec echo hi",
		"python script.py",
		"node script.js",
		"./script.sh --flag",
		"/tmp/script.sh --flag",
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if got := proposedCommandPrefix("bash", map[string]any{"command": command}); got != nil {
				t.Fatalf("unsafe launcher got prefix %#v", got)
			}
		})
	}
}

func TestProposedCommandPrefixRejectsRequestedUnsafeLauncherPrefix(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{
		"command":     "find . -type f",
		"prefix_rule": []any{"find", "."},
	})
	if got != nil {
		t.Fatalf("unsafe requested launcher prefix should be rejected, got %#v", got)
	}
}
