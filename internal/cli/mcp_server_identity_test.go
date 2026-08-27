package cli

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

// ONE CONFIG KEY PER RUNTIME IDENTITY, ENFORCED WHERE THE KEY IS WRITTEN.
//
// Registration trims the config-map key, so "docs" and "  docs" are two entries
// in the file and one server everywhere downstream. They share a tool count and
// a failure, map iteration decides which configuration wins, and each row
// redacts that shared failure with its own credentials, so the row that did not
// fail can print the other one's.
//
// Validation on the way in checks the incoming server by itself and cannot see
// the collision, so the check belongs at the write.
func TestUpsertRefusesAKeyThatCollidesWithAnExistingServer(t *testing.T) {
	cfg := &mcpWritableConfig{}
	cfg.ensureRaw()
	cfg.file.MCP.Servers = map[string]config.MCPServerConfig{
		"  docs": {URL: "https://a.invalid/mcp"},
	}

	_, err := cfg.upsertServer("docs", config.MCPServerConfig{URL: "https://b.invalid/mcp"})
	if err == nil {
		t.Fatalf("a colliding key was written: %#v", cfg.file.MCP.Servers)
	}
	for _, want := range []string{`"docs"`, `"  docs"`, "rename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %s: %v", want, err)
		}
	}
	if len(cfg.file.MCP.Servers) != 1 {
		t.Errorf("the refused server was written anyway: %#v", cfg.file.MCP.Servers)
	}
}

// Updating a server through its own key is the ordinary case and must not be
// mistaken for a collision with itself.
func TestUpsertStillUpdatesTheSameKey(t *testing.T) {
	cfg := &mcpWritableConfig{}
	cfg.ensureRaw()
	cfg.file.MCP.Servers = map[string]config.MCPServerConfig{
		"  docs": {URL: "https://a.invalid/mcp"},
	}
	if _, err := cfg.upsertServer("  docs", config.MCPServerConfig{URL: "https://b.invalid/mcp"}); err != nil {
		t.Fatalf("updating a server through its own key was refused: %v", err)
	}
	if got := cfg.file.MCP.Servers["  docs"].URL; got != "https://b.invalid/mcp" {
		t.Errorf("URL = %q, want the update applied", got)
	}
}

// And an unrelated new server is not blocked by an existing one.
func TestUpsertAllowsADistinctName(t *testing.T) {
	cfg := &mcpWritableConfig{}
	cfg.ensureRaw()
	cfg.file.MCP.Servers = map[string]config.MCPServerConfig{
		"docs": {URL: "https://a.invalid/mcp"},
	}
	if _, err := cfg.upsertServer("search", config.MCPServerConfig{URL: "https://b.invalid/mcp"}); err != nil {
		t.Fatalf("a distinct server was refused: %v", err)
	}
	if len(cfg.file.MCP.Servers) != 2 {
		t.Errorf("servers = %#v, want both", cfg.file.MCP.Servers)
	}
}
