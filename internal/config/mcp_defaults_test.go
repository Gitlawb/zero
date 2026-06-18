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
