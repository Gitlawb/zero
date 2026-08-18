package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDefaultMCPServer(t *testing.T) {
	if !IsDefaultMCPServer("exa") {
		t.Fatal("exa should be a built-in default")
	}
	if IsDefaultMCPServer("  exa  ") == false {
		t.Fatal("IsDefaultMCPServer should trim whitespace")
	}
	if IsDefaultMCPServer("not-a-default") {
		t.Fatal("unknown server should not be a default")
	}
}

func TestResolveMCPSeedsEnabledExaDefault(t *testing.T) {
	cfg, err := ResolveMCP(ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	exa, ok := cfg.Servers["exa"]
	if !ok {
		t.Fatal("expected the exa default to be seeded with no user config")
	}
	if exa.Type != "http" || exa.URL != "https://mcp.exa.ai/mcp" {
		t.Fatalf("unexpected exa default: %#v", exa)
	}
	if exa.Disabled {
		t.Fatal("the exa default must be enabled out of the box")
	}
}

func TestResolveMCPUserCanDisableDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"exa":{"disabled":true}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	if !cfg.Servers["exa"].Disabled {
		t.Fatal("a user must be able to disable the default by writing over it")
	}
}

func TestIsUnconfiguredDefault(t *testing.T) {
	if !IsUnconfiguredDefault("exa", DefaultMCPServers()["exa"]) {
		t.Fatal("an untouched exa default should be reported as unconfigured")
	}
	if IsUnconfiguredDefault("exa", MCPServerConfig{Type: "http", URL: "https://example.com/mcp"}) {
		t.Fatal("a server overriding the default URL is no longer unconfigured")
	}
	if IsUnconfiguredDefault("exa", MCPServerConfig{Type: "http", URL: "https://mcp.exa.ai/mcp", Auth: "bearer"}) {
		t.Fatal("a server with credentials added is no longer unconfigured")
	}
	if IsUnconfiguredDefault("not-a-default", MCPServerConfig{}) {
		t.Fatal("a server with no matching default can never be unconfigured-default")
	}
}

func TestResolveMCPExplicitReenableIsNotUnconfiguredDefault(t *testing.T) {
	// `zero mcp enable exa` after a prior disable writes {"disabled":false}
	// explicitly. The resolved value is identical to the untouched default (both
	// enabled, no credentials), but the user DID take an explicit action here, so
	// IsUnconfiguredDefault must not treat it as untouched (issue #563 review).
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"exa":{"disabled":false}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	exa := cfg.Servers["exa"]
	if exa.Disabled {
		t.Fatalf("explicit re-enable should leave the server enabled: %#v", exa)
	}
	if IsUnconfiguredDefault("exa", exa) {
		t.Fatal("an explicit enable/disable toggle must count as user-configured, even though the resolved value matches the default")
	}
}

func TestResolveMCPExplicitRedeclareOfDefaultValuesIsNotUnconfiguredDefault(t *testing.T) {
	// A user who copies Exa's exact default type/url into their config
	// (e.g. from an example file, planning to add credentials later) produces a
	// resolved value byte-identical to DefaultMCPServers()["exa"] — the
	// same trap TestResolveMCPExplicitReenableIsNotUnconfiguredDefault covers for
	// the disabled toggle. IsUnconfiguredDefault must still treat this as
	// user-configured because the user's JSON declared an entry for it, even
	// though a plain resolved-value comparison could not tell the difference.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"exa":{"type":"http","url":"https://mcp.exa.ai/mcp"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	exa := cfg.Servers["exa"]
	want := DefaultMCPServers()["exa"]
	if exa.Type != want.Type || exa.URL != want.URL || exa.Disabled != want.Disabled {
		t.Fatalf("expected the resolved value to match the default's fields exactly: %#v", exa)
	}
	if IsUnconfiguredDefault("exa", exa) {
		t.Fatal("redeclaring the default's exact values is still an explicit user configuration, not an untouched default")
	}
}

func TestResolveMCPUserCanOverrideDefaultURLKeepingOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	// Point Exa at a proxy; the default's Type must survive.
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"exa":{"url":"https://example.com/mcp"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	exa := cfg.Servers["exa"]
	if exa.URL != "https://example.com/mcp" {
		t.Fatalf("user override of the default URL did not apply: %#v", exa)
	}
	if exa.Type != "http" {
		t.Fatalf("override should keep the default's other fields (type), got %#v", exa)
	}
}
