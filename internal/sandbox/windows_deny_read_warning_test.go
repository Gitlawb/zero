package sandbox

import (
	"strings"
	"testing"
)

func windowsRestrictedTokenBackend() Backend {
	return Backend{
		Name:            BackendWindowsRestrictedToken,
		Platform:        "windows",
		Available:       true,
		NativeIsolation: true,
	}
}

func profileWithDenyRead(paths ...string) PermissionProfile {
	profile := PermissionProfile{}
	profile.FileSystem.DenyRead = paths
	return profile
}

// Setting denyRead on Windows silently costs the token's write jail, because it
// selects the shape without WRITE_RESTRICTED and that shape has to keep the World
// SID. The trade is defensible; making it invisible is not. Someone who asked for
// read-deny has no way to discover they gave up write confinement for it.
func TestDenyReadOnWindowsWarnsThatTheWriteJailIsGone(t *testing.T) {
	warnings := windowsDenyReadWarnings(windowsRestrictedTokenBackend(), profileWithDenyRead(`C:\Users\someone\.config\creds`))
	if len(warnings) == 0 {
		t.Fatal("configuring denyRead on Windows produced no warning, so the lost write jail stays invisible")
	}
	warning := strings.ToLower(strings.Join(warnings, " "))
	// It has to name the cause and the consequence. A warning that says only
	// "degraded" sends the reader to the source to find out what changed.
	for _, want := range []string{"denyread", "write", "#869"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning does not mention %q, so it does not explain the trade: %q", want, warning)
		}
	}
}

// The default Windows posture must stay quiet. Zero never populates denyRead on
// Windows itself, so warning unconditionally would train every user to ignore the
// one case that matters.
func TestDefaultWindowsProfileProducesNoDenyReadWarning(t *testing.T) {
	if warnings := windowsDenyReadWarnings(windowsRestrictedTokenBackend(), PermissionProfile{}); len(warnings) != 0 {
		t.Fatalf("the default Windows profile warned about denyRead it does not set: %v", warnings)
	}
	// Blank and whitespace-only entries are not a configured denyRead either.
	if warnings := windowsDenyReadWarnings(windowsRestrictedTokenBackend(), profileWithDenyRead("", "   ")); len(warnings) != 0 {
		t.Fatalf("empty denyRead entries produced a warning: %v", warnings)
	}
}

// The warning describes one specific token implementation, so it must not appear
// for backends that do not build that token.
func TestDenyReadWarningIsScopedToTheWindowsRestrictedToken(t *testing.T) {
	others := []Backend{
		{Name: BackendMacOSSeatbelt, Platform: "darwin", Available: true, NativeIsolation: true},
		{Name: BackendLinuxLandlock, Platform: "linux", Available: true, NativeIsolation: true},
		// Same backend name but no native isolation: no token is built, so the
		// warning would describe enforcement that is not running at all.
		{Name: BackendWindowsRestrictedToken, Platform: "windows", NativeIsolation: false},
	}
	for _, backend := range others {
		if warnings := windowsDenyReadWarnings(backend, profileWithDenyRead(`C:\secret`)); len(warnings) != 0 {
			t.Errorf("backend %q (nativeIsolation=%v) got the Windows token warning: %v", backend.Name, backend.NativeIsolation, warnings)
		}
	}
}
