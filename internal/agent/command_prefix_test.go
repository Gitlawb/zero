package agent

import (
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestProposedCommandPrefixUsesSafeSimpleCommands(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{"command": "go test ./..."})
	want := []string{"go", "test", "./..."}
	if !equalStringSlices(got, want) {
		t.Fatalf("prefix = %#v, want %#v", got, want)
	}
}

func TestProposedCommandPrefixSupportsExecCommand(t *testing.T) {
	got := proposedCommandPrefix("exec_command", map[string]any{"cmd": "go test ./..."})
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

func TestProposedCommandPrefixSupportsSegmentedCommands(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{"command": "ps aux | head -5"})
	want := []string{"ps", "aux"}
	if !equalStringSlices(got, want) {
		t.Fatalf("prefix = %#v, want %#v", got, want)
	}
}

func TestProposedCommandPrefixHonorsRequestedPrefixAcrossSegments(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{
		"command":     "git status --short && git status --branch",
		"prefix_rule": []any{"git", "status"},
	})
	want := []string{"git", "status"}
	if !equalStringSlices(got, want) {
		t.Fatalf("prefix = %#v, want %#v", got, want)
	}
}

func TestProposedCommandPrefixRejectsRequestedPrefixThatDoesNotCoverSegments(t *testing.T) {
	got := proposedCommandPrefix("bash", map[string]any{
		"command":     "ps aux && npm install",
		"prefix_rule": []any{"ps", "aux"},
	})
	if got != nil {
		t.Fatalf("partial requested prefix should be rejected, got %#v", got)
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

func TestMatchCommandPrefixCoversSegmentedCommandWithSafeTail(t *testing.T) {
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: t.TempDir()})
	engine.GrantCommandPrefixForSession("bash", []string{"ps", "aux"})

	grant, ok, session := matchCommandPrefix("bash", map[string]any{"command": "ps aux | head -5"}, Options{Sandbox: engine})
	if !ok || !session || !equalStringSlices(grant.Prefix, []string{"ps", "aux"}) {
		t.Fatalf("match = %#v ok=%v session=%v, want session ps aux prefix", grant, ok, session)
	}
}

func TestMatchCommandPrefixRejectsUncoveredSegment(t *testing.T) {
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: t.TempDir()})
	engine.GrantCommandPrefixForSession("bash", []string{"ps", "aux"})

	if grant, ok, session := matchCommandPrefix("bash", map[string]any{"command": "ps aux && npm install"}, Options{Sandbox: engine}); ok {
		t.Fatalf("match = %#v session=%v, want no match because npm segment is uncovered", grant, session)
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
