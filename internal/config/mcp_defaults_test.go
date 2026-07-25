package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDefaultMCPServer(t *testing.T) {
	if !IsDefaultMCPServer("firecrawl") {
		t.Fatal("firecrawl should be a built-in default")
	}
	if IsDefaultMCPServer("  firecrawl  ") == false {
		t.Fatal("IsDefaultMCPServer should trim whitespace")
	}
	if IsDefaultMCPServer("not-a-default") {
		t.Fatal("unknown server should not be a default")
	}
}

func TestResolveMCPSeedsEnabledFirecrawlDefault(t *testing.T) {
	cfg, err := ResolveMCP(ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	firecrawl, ok := cfg.Servers["firecrawl"]
	if !ok {
		t.Fatal("expected the firecrawl default to be seeded with no user config")
	}
	if firecrawl.Type != "http" || firecrawl.URL != "https://mcp.firecrawl.dev/v2/mcp" {
		t.Fatalf("unexpected firecrawl default: %#v", firecrawl)
	}
	if firecrawl.Disabled {
		t.Fatal("the firecrawl default must be enabled out of the box")
	}
}

func TestResolveMCPUserCanDisableDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"firecrawl":{"disabled":true}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	if !cfg.Servers["firecrawl"].Disabled {
		t.Fatal("a user must be able to disable the default by writing over it")
	}
}

func TestIsUnconfiguredDefault(t *testing.T) {
	if !IsUnconfiguredDefault("firecrawl", DefaultMCPServers()["firecrawl"]) {
		t.Fatal("an untouched firecrawl default should be reported as unconfigured")
	}
	if IsUnconfiguredDefault("firecrawl", MCPServerConfig{Type: "http", URL: "http://localhost:3002/mcp"}) {
		t.Fatal("a server overriding the default URL is no longer unconfigured")
	}
	if IsUnconfiguredDefault("firecrawl", MCPServerConfig{Type: "http", URL: "https://mcp.firecrawl.dev/v2/mcp", Auth: "bearer"}) {
		t.Fatal("a server with credentials added is no longer unconfigured")
	}
	if IsUnconfiguredDefault("not-a-default", MCPServerConfig{}) {
		t.Fatal("a server with no matching default can never be unconfigured-default")
	}
}

func TestResolveMCPExplicitReenableIsNotUnconfiguredDefault(t *testing.T) {
	// `zero mcp enable firecrawl` after a prior disable writes {"disabled":false}
	// explicitly. The resolved value is identical to the untouched default (both
	// enabled, no credentials), but the user DID take an explicit action here, so
	// IsUnconfiguredDefault must not treat it as untouched (issue #563 review).
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"firecrawl":{"disabled":false}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	firecrawl := cfg.Servers["firecrawl"]
	if firecrawl.Disabled {
		t.Fatalf("explicit re-enable should leave the server enabled: %#v", firecrawl)
	}
	if IsUnconfiguredDefault("firecrawl", firecrawl) {
		t.Fatal("an explicit enable/disable toggle must count as user-configured, even though the resolved value matches the default")
	}
}

func TestResolveMCPExplicitRedeclareOfDefaultValuesIsNotUnconfiguredDefault(t *testing.T) {
	// A user who copies firecrawl's exact default type/url into their config
	// (e.g. from an example file, planning to add credentials later) produces a
	// resolved value byte-identical to DefaultMCPServers()["firecrawl"] — the
	// same trap TestResolveMCPExplicitReenableIsNotUnconfiguredDefault covers for
	// the disabled toggle. IsUnconfiguredDefault must still treat this as
	// user-configured because the user's JSON declared an entry for it, even
	// though a plain resolved-value comparison could not tell the difference.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"firecrawl":{"type":"http","url":"https://mcp.firecrawl.dev/v2/mcp"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	firecrawl := cfg.Servers["firecrawl"]
	want := DefaultMCPServers()["firecrawl"]
	if firecrawl.Type != want.Type || firecrawl.URL != want.URL || firecrawl.Disabled != want.Disabled {
		t.Fatalf("expected the resolved value to match the default's fields exactly: %#v", firecrawl)
	}
	if IsUnconfiguredDefault("firecrawl", firecrawl) {
		t.Fatal("redeclaring the default's exact values is still an explicit user configuration, not an untouched default")
	}
}

func TestResolveMCPUserCanOverrideDefaultURLKeepingOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	// Point firecrawl at a self-hosted instance; the default's Type must survive.
	if err := os.WriteFile(path, []byte(`{"mcp":{"servers":{"firecrawl":{"url":"http://localhost:3002/mcp"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveMCP(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("ResolveMCP: %v", err)
	}
	firecrawl := cfg.Servers["firecrawl"]
	if firecrawl.URL != "http://localhost:3002/mcp" {
		t.Fatalf("user override of the default URL did not apply: %#v", firecrawl)
	}
	if firecrawl.Type != "http" {
		t.Fatalf("override should keep the default's other fields (type), got %#v", firecrawl)
	}
}
