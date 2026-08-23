package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
)

// The gate and the tool must resolve a path argument to the SAME bytes.
// aliasedStringArg does not trim, so while requestPaths ran path args through
// argString (strings.TrimSpace), a token file whose name carries meaningful
// whitespace was protected under its real spelling while the gate inspected the
// trimmed one — read_file opened "bridge-token " after the gate cleared
// "bridge-token". This runs the production path (registry.RunWithOptions with
// the sandbox engine), not a profile builder, because that divergence is
// invisible to a test that calls the gate directly.
func TestEngineDeniesReadFileWithExactSpacedTokenPath(t *testing.T) {
	for _, tokenName := range []string{"bridge-token ", " bridge-token", "bridge token "} {
		t.Run(strings.ReplaceAll(tokenName, " ", "_"), func(t *testing.T) {
			ws, _, engine := daemonTokenFixtureNamed(t, tokenName)

			registry := NewRegistry()
			registry.Register(NewScopedReadFileTool(ws, nil))

			// Send the RELATIVE spelling. The whitespace has to sit at the
			// boundary of the argument string for TrimSpace to reach it: in an
			// absolute path the space is mid-string (after the separator) and
			// the old gate happened to behave. That is exactly why this needs
			// to be a named regression rather than a variant of the existing
			// absolute-path coverage.
			result := registry.RunWithOptions(context.Background(), "read_file",
				map[string]any{"path": tokenName}, RunOptions{Sandbox: engine})
			if result.Status == StatusOK {
				t.Fatalf("read_file served the protected token under its exact spelling: output=%q", result.Output)
			}
			if strings.Contains(result.Output, "bridge-secret") {
				t.Fatalf("denied read still leaked the bearer token: output=%q", result.Output)
			}

			// The trimmed spelling must stay denied too: it is the same
			// credential identity, and the gate is now a superset of what it
			// inspected before this became exact.
			trimmed := strings.TrimSpace(tokenName)
			trimmedResult := registry.RunWithOptions(context.Background(), "read_file",
				map[string]any{"path": trimmed}, RunOptions{Sandbox: engine})
			if strings.Contains(trimmedResult.Output, "bridge-secret") {
				t.Fatalf("trimmed spelling leaked the bearer token: output=%q", trimmedResult.Output)
			}
		})
	}
}

// The Step C matrix: every tool that can name a path, crossed with the
// spellings an attacker controls. Earlier rounds on this branch each closed one
// cell — shell, then read_file, then apply_patch headers, then whitespace — so
// the point here is to run them all through the production entrypoint at once
// and make a future gap fail as a missing row rather than as a new report.
func TestDaemonTokenProtectionMatrix(t *testing.T) {
	// Each column is a spelling of the SAME protected credential.
	spellings := []struct {
		name  string
		token string // token filename on disk
		arg   func(ws, token string) string
	}{
		{
			name:  "exact",
			token: "bridge-token",
			arg:   func(_, token string) string { return token },
		},
		{
			name:  "trailing space",
			token: "bridge-token ",
			arg:   func(_, token string) string { return token },
		},
		{
			name:  "relative",
			token: "bridge-token",
			arg:   func(_, token string) string { return filepath.Base(token) },
		},
		{
			name:  "dot segment",
			token: "bridge-token",
			arg: func(ws, token string) string {
				return filepath.Join(ws, ".", filepath.Base(token))
			},
		},
		{
			name:  "parent traversal",
			token: "bridge-token",
			arg: func(ws, token string) string {
				return filepath.Join(ws, "sub", "..", filepath.Base(token))
			},
		},
	}

	for _, spelling := range spellings {
		t.Run(spelling.name, func(t *testing.T) {
			t.Run("read_file", func(t *testing.T) {
				ws, token, engine := daemonTokenFixtureNamed(t, spelling.token)
				registry := NewRegistry()
				registry.Register(NewScopedReadFileTool(ws, nil))
				result := registry.RunWithOptions(context.Background(), "read_file",
					map[string]any{"path": spelling.arg(ws, token)}, RunOptions{Sandbox: engine})
				assertTokenNotLeaked(t, "read_file", result)
			})

			t.Run("write_file", func(t *testing.T) {
				ws, token, engine := daemonTokenFixtureNamed(t, spelling.token)
				registry := NewRegistry()
				registry.Register(NewScopedWriteFileTool(ws, nil))
				result := registry.RunWithOptions(context.Background(), "write_file",
					map[string]any{"path": spelling.arg(ws, token), "content": "attacker\n"},
					RunOptions{Sandbox: engine})
				if result.Status == StatusOK {
					t.Fatalf("write_file overwrote the protected token: output=%q", result.Output)
				}
				// A refused write must leave the bearer intact, not truncate it.
				contents, err := os.ReadFile(token)
				if err != nil || string(contents) != "bridge-secret\n" {
					t.Fatalf("token changed after a denied write: contents=%q err=%v", contents, err)
				}
			})

			t.Run("list_directory", func(t *testing.T) {
				ws, _, engine := daemonTokenFixtureNamed(t, spelling.token)
				registry := NewRegistry()
				registry.Register(NewScopedListDirectoryTool(ws, nil))
				result := registry.RunWithOptions(context.Background(), "list_directory",
					map[string]any{"path": ws}, RunOptions{Sandbox: engine})
				if strings.Contains(result.Output, "bridge-token") {
					t.Fatalf("list_directory surfaced the protected token filename:\n%s", result.Output)
				}
				if !strings.Contains(result.Output, "main.go") {
					t.Fatalf("list_directory dropped ordinary entries while filtering:\n%s", result.Output)
				}
			})

			t.Run("grep", func(t *testing.T) {
				ws, _, engine := daemonTokenFixtureNamed(t, spelling.token)
				grep, ok := NewScopedGrepTool(ws, nil).(sandboxAwareTool)
				if !ok {
					t.Fatal("grep tool must be sandbox-aware")
				}
				result := grep.RunWithSandbox(context.Background(), map[string]any{
					"pattern":     "bridge-secret",
					"output_mode": "files_with_matches",
				}, engine)
				if result.Status != StatusOK {
					t.Fatalf("grep failed: %s", result.Output)
				}
				if strings.Contains(result.Output, "bridge-token") {
					t.Fatalf("grep surfaced the protected token:\n%s", result.Output)
				}
				if !strings.Contains(result.Output, "main.go") {
					t.Fatalf("grep dropped ordinary matches while filtering:\n%s", result.Output)
				}
			})

			t.Run("apply_patch", func(t *testing.T) {
				ws, token, engine := daemonTokenFixtureNamed(t, spelling.token)
				registry := NewRegistry()
				registry.Register(NewScopedApplyPatchTool(ws, nil))
				target := spelling.arg(ws, token)
				patch := "*** Begin Patch\n*** Update File: " + target +
					"\n@@\n-bridge-secret\n+attacker\n*** End Patch\n"
				result := registry.RunWithOptions(context.Background(), "apply_patch",
					map[string]any{"patch": patch}, RunOptions{Sandbox: engine})
				if result.Status == StatusOK {
					t.Fatalf("apply_patch rewrote the protected token: output=%q", result.Output)
				}
				contents, err := os.ReadFile(token)
				if err != nil || string(contents) != "bridge-secret\n" {
					t.Fatalf("token changed after a denied patch: contents=%q err=%v", contents, err)
				}
				// WHICH layer refused is the point of the row. "The executor
				// tripped over a path that does not exist" is not protection,
				// so require the refusal to come from the sandbox.
				if !strings.HasPrefix(result.Output, "Sandbox block") {
					t.Fatalf("apply_patch was not refused by the sandbox gate: output=%q", result.Output)
				}
				headerPaths, err := sandbox.PatchHeaderPaths(patch)
				if err != nil {
					t.Fatalf("PatchHeaderPaths: %v", err)
				}
				switch {
				case strings.TrimSpace(target) != target:
					// A structured patch cannot name THIS token at all: the
					// sandbox's header parser and the executor's both trim the
					// header, so the patch describes "bridge-token" — a file
					// that does not exist — and the token is unreachable rather
					// than gate-denied. Pin that agreement explicitly, because
					// if either parser stopped trimming, the gate would inspect
					// a different name than the executor opens, which is the
					// exact divergence this branch was opened for.
					if len(headerPaths) != 1 || headerPaths[0] != filepath.ToSlash(strings.TrimSpace(target)) {
						t.Fatalf("patch header paths = %q, want only the trimmed spelling the executor opens", headerPaths)
					}
				case !filepath.IsAbs(target):
					// An in-workspace relative spelling reaches the credential
					// gate, which is the layer that must refuse it. (An absolute
					// spelling is refused one step earlier, as out-of-workspace.)
					if !strings.Contains(result.Output, "holds the remote bridge token") {
						t.Fatalf("apply_patch refusal did not come from the credential gate: output=%q", result.Output)
					}
				}
			})
		})
	}
}

// registry.Run reaches list_directory without a sandbox engine (MCP, legacy
// callers). The protected-credential set does not come from a policy, so the
// bearer filename must not become visible just because no engine was passed.
func TestListDirectoryWithoutEngineStillHidesProtectedToken(t *testing.T) {
	ws, _, _ := daemonTokenFixture(t)

	registry := NewRegistry()
	registry.Register(NewScopedListDirectoryTool(ws, nil))
	result := registry.Run(context.Background(), "list_directory", map[string]any{"path": ws})

	if strings.Contains(result.Output, "bridge-token") {
		t.Fatalf("engine-less list_directory disclosed the protected token filename:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Fatalf("engine-less list_directory dropped ordinary entries while filtering:\n%s", result.Output)
	}
}

func assertTokenNotLeaked(t *testing.T, tool string, result Result) {
	t.Helper()
	if result.Status == StatusOK {
		t.Fatalf("%s served the protected token: output=%q", tool, result.Output)
	}
	if strings.Contains(result.Output, "bridge-secret") {
		t.Fatalf("%s leaked the bearer token in a denial: output=%q", tool, result.Output)
	}
}
