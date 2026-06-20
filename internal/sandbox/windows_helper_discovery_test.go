package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestResolveWindowsSandboxHelperTiers covers the three-tier resolution that
// makes the Windows sandbox reachable in dev (self-dispatch) while preserving
// the release layout (adjacent .exe) and PATH discovery.
func TestResolveWindowsSandboxHelperTiers(t *testing.T) {
	restore := osExecutable
	defer func() { osExecutable = restore }()

	t.Run("tier1 adjacent exe wins", func(t *testing.T) {
		dir := t.TempDir()
		adjacent := filepath.Join(dir, WindowsSandboxCommandRunnerName)
		writeFile(t, adjacent)
		osExecutable = func() (string, error) { return filepath.Join(dir, "zero.exe"), nil }
		got := ResolveWindowsSandboxCommandRunner(func(string) (string, error) {
			return `C:\on-path\runner.exe`, nil // must NOT be chosen — adjacent wins
		})
		if got.Name != adjacent || len(got.ArgsPrefix) != 0 {
			t.Fatalf("tier1 = %#v, want adjacent exe with no prefix", got)
		}
	})

	t.Run("tier2 PATH when no adjacent", func(t *testing.T) {
		dir := t.TempDir()
		osExecutable = func() (string, error) { return filepath.Join(dir, "zero.exe"), nil }
		got := ResolveWindowsSandboxCommandRunner(func(name string) (string, error) {
			if name == WindowsSandboxCommandRunnerName {
				return `C:\on-path\runner.exe`, nil
			}
			return "", errors.New("missing")
		})
		if got.Name != `C:\on-path\runner.exe` || len(got.ArgsPrefix) != 0 {
			t.Fatalf("tier2 = %#v, want PATH exe with no prefix", got)
		}
	})

	t.Run("tier3 self-dispatch when nothing found", func(t *testing.T) {
		dir := t.TempDir()
		self := filepath.Join(dir, "zero.exe")
		osExecutable = func() (string, error) { return self, nil }
		got := ResolveWindowsSandboxCommandRunner(func(string) (string, error) {
			return "", errors.New("missing")
		})
		if got.Name != self {
			t.Fatalf("tier3 name = %q, want self-dispatch binary %q", got.Name, self)
		}
		if len(got.ArgsPrefix) != 1 || got.ArgsPrefix[0] != WindowsCommandRunnerSubcommand {
			t.Fatalf("tier3 prefix = %#v, want [%q]", got.ArgsPrefix, WindowsCommandRunnerSubcommand)
		}
		if !got.Available() {
			t.Fatal("tier3 helper should be Available")
		}
	})

	t.Run("setup helper uses its own subcommand", func(t *testing.T) {
		dir := t.TempDir()
		osExecutable = func() (string, error) { return filepath.Join(dir, "zero.exe"), nil }
		got := ResolveWindowsSandboxSetupHelper(func(string) (string, error) {
			return "", errors.New("missing")
		})
		if len(got.ArgsPrefix) != 1 || got.ArgsPrefix[0] != WindowsSandboxSetupSubcommand {
			t.Fatalf("setup prefix = %#v, want [%q]", got.ArgsPrefix, WindowsSandboxSetupSubcommand)
		}
	})

	t.Run("unavailable only when os.Executable fails", func(t *testing.T) {
		osExecutable = func() (string, error) { return "", errors.New("no exe") }
		got := ResolveWindowsSandboxCommandRunner(func(string) (string, error) {
			return "", errors.New("missing")
		})
		if got.Available() {
			t.Fatalf("helper = %#v, want unavailable when os.Executable fails", got)
		}
	})
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
