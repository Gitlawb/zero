package agent

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
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
	if runtime.GOOS == "windows" {
		// head is MSYS-prone on Windows (#458), so proposedCommandPrefix must
		// not offer "ps aux" as a reusable prefix here: approving it would
		// escalate the whole command, including the uncovered head segment,
		// to bypass the sandbox unreviewed. See
		// TestProposedCommandPrefixRejectsPrefixLeavingUnsafeTailUncovered for
		// the platform-independent regression coverage of this behavior.
		if got != nil {
			t.Fatalf("expected no prefix on Windows because head is MSYS-prone, got %#v", got)
		}
		return
	}
	want := []string{"ps", "aux"}
	if !equalStringSlices(got, want) {
		t.Fatalf("prefix = %#v, want %#v", got, want)
	}
}

// TestProposedCommandPrefixRejectsPrefixLeavingUnsafeTailUncovered guards
// against proposedCommandPrefix offering to approve one segment of a
// multi-segment command (e.g. "ps aux") while a different segment (e.g. "npm
// install") is not known-safe. shellExecutionArgsForApproval escalates the
// entire command once any prefix is approved, so an uncovered unsafe segment
// would bypass the sandbox unreviewed. Uses npm, which is never known-safe on
// any platform, so the assertion does not depend on runtime.GOOS.
func TestProposedCommandPrefixRejectsPrefixLeavingUnsafeTailUncovered(t *testing.T) {
	if got := proposedCommandPrefix("bash", map[string]any{"command": "ps aux && npm install"}); got != nil {
		t.Fatalf("expected no prefix because npm segment is not known-safe, got %#v", got)
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
	// head is MSYS-prone on Windows (#458) and must not count as a known-safe tail.
	command := "ps aux | head -5"
	if runtime.GOOS == "windows" {
		command = "ps aux | echo ok"
	}

	grant, ok, session := matchCommandPrefix("bash", map[string]any{"command": command}, Options{Sandbox: engine})
	if !ok || !session || !equalStringSlices(grant.Prefix, []string{"ps", "aux"}) {
		t.Fatalf("match = %#v ok=%v session=%v, want session ps aux prefix", grant, ok, session)
	}
}

func TestMatchCommandPrefixRejectsGrantWhenCdEscapesProject(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "grants.json")})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: root, Store: store})
	if _, err := engine.GrantCommandPrefixForProject(sandbox.CommandPrefixInput{ToolName: "bash", Prefix: []string{"go", "test"}}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	// `cd` into another project must not let the project-scoped `go test` grant
	// authorize an unsandboxed run outside the granted project.
	command := "cd " + outside + " && go test ./..."
	if grant, ok, _ := matchCommandPrefix("bash", map[string]any{"command": command}, Options{Sandbox: engine}); ok {
		t.Fatalf("expected no match for a cd that escapes the project, got %#v", grant)
	}

	// A `cd` that stays inside the project still honors the grant.
	inside := "cd sub && go test ./..."
	if _, ok, _ := matchCommandPrefix("bash", map[string]any{"command": inside}, Options{Sandbox: engine}); !ok {
		t.Fatal("expected a within-project cd to still match the grant")
	}

	// A non-static cd target (home) cannot be proven in-project, so it is refused.
	if _, ok, _ := matchCommandPrefix("bash", map[string]any{"command": "cd && go test ./..."}, Options{Sandbox: engine}); ok {
		t.Fatal("expected a bare `cd` (home) to refuse the grant")
	}
}

func TestKnownSafeCommandSegmentRejectsMsysProneOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only known-safe MSYS guard")
	}
	for _, command := range [][]string{{"head", "-5"}, {"cat", "file.txt"}, {"grep", "pat"}} {
		if knownSafeCommandSegment(command) {
			t.Fatalf("expected %q to be unsafe on Windows, got known-safe", command)
		}
	}
	if !knownSafeCommandSegment([]string{"echo", "ok"}) {
		t.Fatal("expected echo to remain known-safe on Windows")
	}
	if !tools.MsysProneCommandName("head") {
		t.Fatal("expected head to be MSYS-prone")
	}
}

func TestPersistCommandPrefixGrantScopedOrSessionFallsBackToSession(t *testing.T) {
	// A store with no workspace root cannot scope a project grant, so the project
	// path must fall back to a session grant instead of recording nothing — a later
	// matching command then reuses the approval rather than prompting again.
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "grants.json")})
	if err != nil {
		t.Fatalf("new grant store: %v", err)
	}
	engine := sandbox.NewEngine(sandbox.EngineOptions{Store: store})
	options := Options{Sandbox: engine}

	prefix := persistCommandPrefixGrantScopedOrSession(PermissionDecisionAllowPrefixProject, "bash", []string{"yarn", "test:unit"}, "reason", options)
	if !equalStringSlices(prefix, []string{"yarn", "test:unit"}) {
		t.Fatalf("fallback prefix = %#v, want [yarn test:unit]", prefix)
	}
	if _, ok := engine.LookupCommandPrefixForSession("bash", []string{"yarn", "test:unit"}); !ok {
		t.Fatal("expected session grant recorded after project scope failed")
	}
	// Nothing was persisted at project/global scope.
	if grants, err := store.ListCommandPrefixes(); err != nil {
		t.Fatalf("list command prefixes: %v", err)
	} else if len(grants) != 0 {
		t.Fatalf("expected no persisted grant, got %#v", grants)
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

func TestCommandPrefixLadderOffersBreadthChoices(t *testing.T) {
	// test:unit has a namespace separator, so the ladder offers the intra-token
	// wildcard alongside the exact prefix. The one-token rung ({"yarn"}) is never
	// offered: a bare launcher grant would approve every later yarn subcommand.
	got := commandPrefixLadder("bash", map[string]any{"command": "yarn test:unit"})
	want := [][]string{
		{"yarn", "test:*"},
		{"yarn", "test:unit"},
	}
	if len(got) != len(want) {
		t.Fatalf("ladder = %#v, want %#v", got, want)
	}
	for index := range want {
		if !equalStringSlices(got[index], want[index]) {
			t.Fatalf("ladder[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestCommandPrefixLadderExcludesOneTokenRung(t *testing.T) {
	// A three-token command still offers the two-token shorter breadth, but never
	// the one-token launcher rung ({"docker"}).
	got := commandPrefixLadder("bash", map[string]any{"command": "docker compose up"})
	want := [][]string{
		{"docker", "compose"},
		{"docker", "compose", "up"},
	}
	if len(got) != len(want) {
		t.Fatalf("ladder = %#v, want %#v", got, want)
	}
	for index := range want {
		if !equalStringSlices(got[index], want[index]) {
			t.Fatalf("ladder[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestCommandPrefixLadderNilForSingleTokenCommand(t *testing.T) {
	// A single-token prefix has no broader/narrower breadth to choose between.
	if got := commandPrefixLadder("bash", map[string]any{"command": "go"}); got != nil {
		t.Fatalf("expected no ladder for single-token command, got %#v", got)
	}
}

func TestIntraTokenWildcardPrefix(t *testing.T) {
	got, ok := intraTokenWildcardPrefix([]string{"yarn", "test:unit"})
	if !ok || !equalStringSlices(got, []string{"yarn", "test:*"}) {
		t.Fatalf("wildcard = %#v ok=%v, want [yarn test:*]", got, ok)
	}
	// A nested name keeps its deepest namespace segment (last separator wins).
	if got, ok := intraTokenWildcardPrefix([]string{"yarn", "test:unit:fast"}); !ok || !equalStringSlices(got, []string{"yarn", "test:unit:*"}) {
		t.Fatalf("nested wildcard = %#v ok=%v, want [yarn test:unit:*]", got, ok)
	}
	if _, ok := intraTokenWildcardPrefix([]string{"yarn", "test"}); ok {
		t.Fatal("token without a separator must not produce a wildcard")
	}
	if _, ok := intraTokenWildcardPrefix([]string{"yarn"}); ok {
		t.Fatal("a lone launcher token must never be wildcarded")
	}
}

func TestGrantPrefixForDecisionHonorsOfferedChoice(t *testing.T) {
	request := PermissionRequest{
		CommandPrefix:        []string{"yarn", "test:unit"},
		CommandPrefixOptions: [][]string{{"yarn", "test:*"}, {"yarn", "test:unit"}},
	}
	// The intra-token wildcard breadth is honored.
	if got := grantPrefixForDecision(request, PermissionDecision{CommandPrefix: []string{"yarn", "test:*"}}); !equalStringSlices(got, []string{"yarn", "test:*"}) {
		t.Fatalf("expected wildcard breadth honored, got %#v", got)
	}
	// A one-token breadth is never offered, so it falls back to the default.
	if got := grantPrefixForDecision(request, PermissionDecision{CommandPrefix: []string{"yarn"}}); !equalStringSlices(got, []string{"yarn", "test:unit"}) {
		t.Fatalf("expected default prefix on unoffered one-token choice, got %#v", got)
	}
	// An empty choice falls back to the request default.
	if got := grantPrefixForDecision(request, PermissionDecision{}); !equalStringSlices(got, []string{"yarn", "test:unit"}) {
		t.Fatalf("expected default prefix on empty choice, got %#v", got)
	}
	// A choice that was never offered falls back to the default (no widening).
	if got := grantPrefixForDecision(request, PermissionDecision{CommandPrefix: []string{"yarn", "install"}}); !equalStringSlices(got, []string{"yarn", "test:unit"}) {
		t.Fatalf("expected default prefix on unoffered choice, got %#v", got)
	}
}
