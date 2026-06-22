package sandbox

import (
	"runtime"
	"strings"
	"testing"
)

// ensureMacToolPaths must make Homebrew / usr-local bin dirs reachable without
// duplicating entries the inherited PATH already has.
func TestEnsureMacToolPaths(t *testing.T) {
	const allFour = "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/local/sbin"

	t.Run("empty path gets the tool dirs", func(t *testing.T) {
		if got := ensureMacToolPaths(""); got != allFour {
			t.Fatalf("ensureMacToolPaths(\"\") = %q, want %q", got, allFour)
		}
	})

	t.Run("scrubbed default path is prefixed, original kept", func(t *testing.T) {
		got := ensureMacToolPaths("/usr/bin:/bin:/usr/sbin:/sbin")
		want := allFour + ":/usr/bin:/bin:/usr/sbin:/sbin"
		if got != want {
			t.Fatalf("ensureMacToolPaths(default) = %q, want %q", got, want)
		}
	})

	t.Run("existing tool dir is not duplicated", func(t *testing.T) {
		got := ensureMacToolPaths("/opt/homebrew/bin:/usr/bin")
		// /opt/homebrew/bin was already present, so only the other three prepend.
		want := "/opt/homebrew/sbin:/usr/local/bin:/usr/local/sbin:/opt/homebrew/bin:/usr/bin"
		if got != want {
			t.Fatalf("ensureMacToolPaths(partial) = %q, want %q", got, want)
		}
		if strings.Count(got, "/opt/homebrew/bin") != 1 {
			t.Fatalf("ensureMacToolPaths duplicated /opt/homebrew/bin: %q", got)
		}
	})

	t.Run("path that already has every tool dir is unchanged", func(t *testing.T) {
		input := allFour + ":/usr/bin"
		if got := ensureMacToolPaths(input); got != input {
			t.Fatalf("ensureMacToolPaths(complete) = %q, want unchanged %q", got, input)
		}
	})
}

// The macOS read roots must cover the whole user-tool trees so an interpreter in
// .../bin and its Cellar/opt dylibs are readable under a restricted (workspace-
// enforced) policy — not just the bare lib dirs.
func TestMacosPlatformReadRootsCoverUserToolTrees(t *testing.T) {
	roots := macosPlatformReadRoots()
	for _, want := range []string{"/opt/homebrew", "/usr/local"} {
		found := false
		for _, r := range roots {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("macosPlatformReadRoots() missing %q (needed so /opt/homebrew/bin + Cellar dylibs are readable); got %v", want, roots)
		}
	}
}

// A Homebrew/usr-local binary loads its own dylibs (node -> libnode/libuv,
// python3 -> its framework), so the seatbelt profile must map-executable those
// trees or the binary can't start even when it is on PATH and readable.
func TestSeatbeltProfileMapsUserToolDylibs(t *testing.T) {
	rules := seatbeltPlatformRuntimeRules()
	if !strings.Contains(rules, "file-map-executable") {
		t.Fatal("seatbeltPlatformRuntimeRules has no file-map-executable rule")
	}
	for _, want := range []string{`(subpath "/opt/homebrew")`, `(subpath "/usr/local")`} {
		if !strings.Contains(rules, want) {
			t.Errorf("seatbelt file-map-executable rule missing %s", want)
		}
	}
}

// realpath()/getcwd() lstat every ancestor of a tool's path, so the profile must
// grant file-read-metadata on the ancestors of the user-tool trees (/opt, /usr).
// Without this a Homebrew python3 dies at startup with
// "realpath: /opt/homebrew/bin/: Operation not permitted" even though the tree is
// otherwise readable — the (subpath ...) read rule does not cover /opt itself.
func TestSeatbeltProfileAllowsUserToolAncestorMetadata(t *testing.T) {
	rules := seatbeltPlatformRuntimeRules()
	for _, want := range []string{`(path-ancestors "/opt/homebrew")`, `(path-ancestors "/usr/local")`} {
		if !strings.Contains(rules, want) {
			t.Errorf("seatbelt profile missing ancestor-metadata rule %s (realpath of a Homebrew tool would be denied)", want)
		}
	}
	if !strings.Contains(rules, "file-read-metadata") {
		t.Fatal("expected a file-read-metadata rule for tool ancestors")
	}
}

// On macOS the sandbox environment preserves the caller env while still exposing
// the Homebrew bin dir so `python3`/`node` resolve to the user's install, not a
// system stub.
func TestSandboxEnvironmentExposesHomebrewOnMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("PATH augmentation is macOS-only")
	}
	env := sandboxEnvironment(Policy{}, BackendMacOSSeatbelt, t.TempDir())
	path := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
			break
		}
	}
	if !strings.Contains(path, "/opt/homebrew/bin") {
		t.Fatalf("sandbox PATH = %q, want it to include /opt/homebrew/bin", path)
	}
}
