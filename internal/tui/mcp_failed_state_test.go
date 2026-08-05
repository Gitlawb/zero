package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
)

// The panel has to report what is running, not what is written down. MCP
// registration is best-effort — a server that fails to start is recorded and
// startup continues — so without the skipped set the panel calls a server that
// never connected "enabled" and the user has no way to tell from here why its
// tools are missing.
func TestBuildMCPViewStateReportsServersThatFailedToStart(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs":    {Type: "stdio", Command: "docs-mcp"},
		"linear":  {Type: "http", URL: "https://linear.example/mcp"},
		"offline": {Type: "http", URL: "https://offline.example/mcp", Disabled: true},
	}}
	skipped := []mcp.SkippedServer{
		{Name: "docs", Err: errors.New(`exec: "docs-mcp": executable file not found in $PATH`)},
		// Disabled servers are never started, so one should not appear here.
		// Assert the precedence anyway: if it ever does, the user turned this
		// server off and "failed" would be a lie.
		{Name: "offline", Err: errors.New("should not be reported")},
	}

	state := BuildMCPViewState(MCPStateOptions{Config: cfg, Skipped: skipped})

	byName := make(map[string]MCPServerView, len(state.Servers))
	for _, server := range state.Servers {
		byName[server.Name] = server
	}
	if got := byName["docs"]; got.State != "failed" ||
		got.Error != `exec: "docs-mcp": executable file not found in $PATH` {
		t.Errorf("failed server = %#v, want state \"failed\" carrying the recorded reason", got)
	}
	if got := byName["linear"]; got.State != "enabled" || got.Error != "" {
		t.Errorf("healthy server = %#v, want it left as enabled with no error", got)
	}
	if got := byName["offline"]; got.State != "disabled" || got.Error != "" {
		t.Errorf("disabled server = %#v, want disabled to win over a recorded failure", got)
	}
}

// The reason is rendered from an error the server produced, so it is untrusted
// text that can carry whatever the transport echoed back — including the
// credential Zero sent it.
func TestBuildMCPViewStateRedactsTheFailureReason(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"linear": {Type: "http", URL: "https://linear.example/mcp"},
	}}
	state := BuildMCPViewState(MCPStateOptions{
		Config: cfg,
		Skipped: []mcp.SkippedServer{{
			Name: "linear",
			Err:  errors.New("handshake rejected: Authorization: Bearer sk-live-abcdef0123456789abcdef"),
		}},
	})

	reason := state.Servers[0].Error
	if strings.Contains(reason, "sk-live-abcdef0123456789abcdef") {
		t.Fatalf("failure reason leaked the bearer token: %q", reason)
	}
	if !strings.Contains(reason, "handshake rejected") {
		t.Errorf("redaction ate the diagnostic part of the reason: %q", reason)
	}
}

// A nil or blank error still means the server is not running. "failed" with
// nothing after it reads like a rendering bug, so fall back to a plain
// statement rather than an empty line.
func TestBuildMCPViewStateFallsBackWhenTheFailureHasNoMessage(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp"},
	}}
	for name, err := range map[string]error{
		"nil error":   nil,
		"blank error": errors.New("   "),
	} {
		t.Run(name, func(t *testing.T) {
			state := BuildMCPViewState(MCPStateOptions{
				Config:  cfg,
				Skipped: []mcp.SkippedServer{{Name: "docs", Err: err}},
			})
			got := state.Servers[0]
			if got.State != "failed" {
				t.Fatalf("state = %q, want \"failed\" even without a message", got.State)
			}
			if strings.TrimSpace(got.Error) == "" {
				t.Error("failed server rendered with no reason at all")
			}
		})
	}
}

// The reason has to survive into the text the user actually reads.
func TestRenderMCPViewShowsTheFailureReason(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp"},
	}}
	state := BuildMCPViewState(MCPStateOptions{
		Config:  cfg,
		Skipped: []mcp.SkippedServer{{Name: "docs", Err: errors.New("connection refused")}},
	})

	rendered := renderMCPView(state, 100)
	if !strings.Contains(rendered, "failed") {
		t.Errorf("panel does not say the server failed:\n%s", rendered)
	}
	if !strings.Contains(rendered, "connection refused") {
		t.Errorf("panel does not show why it failed:\n%s", rendered)
	}
	if strings.Contains(rendered, "docs · enabled") {
		t.Errorf("panel still calls the failed server enabled:\n%s", rendered)
	}
}

// End to end through the model: what startup recorded is what /mcp reports.
// The wiring is the whole point — the builder can be correct while the panel
// still renders from a set nobody handed it.
func TestModelMCPPanelReportsStartupFailures(t *testing.T) {
	m := newModel(context.Background(), Options{
		MCPConfig: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"docs": {Type: "stdio", Command: "docs-mcp"},
		}},
		MCPSkipped: []mcp.SkippedServer{
			{Name: "docs", Err: errors.New("connection refused")},
		},
	})
	panel := m.mcpText()
	if !strings.Contains(panel, "failed") || !strings.Contains(panel, "connection refused") {
		t.Errorf("/mcp panel did not carry the startup failure through:\n%s", panel)
	}
}

// The failure reason is the one string on this panel an MCP server writes
// itself, and it lands in a terminal. redaction.ErrorMessage removes
// credentials, not control bytes, so a hostile handshake error can clear the
// screen, move the cursor, or forge a row that looks like another server.
//
// Payload is anandh8x's from the #835 review: an escape sequence and a newline
// carrying a line shaped exactly like a real entry.
func TestMCPFailureReasonCannotInjectTerminalControl(t *testing.T) {
	hostile := "connection refused\x1b[2J\n\u203a forged \u00b7 enabled"
	lines := mcpManagerServerLines([]MCPServerView{
		{Name: "evil", Transport: "http", State: "failed", Error: hostile},
	})

	joined := strings.Join(lines, "\n")
	if strings.ContainsRune(joined, '\x1b') {
		t.Errorf("escape byte survived into the rendered panel:\n%q", joined)
	}
	for _, line := range lines {
		if strings.ContainsAny(line, "\r\n") {
			t.Errorf("a rendered line carries its own newline, so it occupies rows the panel did not count:\n%q", line)
		}
	}
	// The forged text may appear as inert characters, but never as its own row:
	// that is what makes it read as a second server.
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "\u203a forged") {
			t.Errorf("hostile reason forged a server row: %q", line)
		}
	}
	// The real reason must still reach the user; sanitizing must not blank it.
	if !strings.Contains(joined, "connection refused") {
		t.Errorf("the actual failure reason was lost:\n%q", joined)
	}
}

// A server that returns megabytes of error text must not push the rest of the
// panel off screen.
func TestMCPFailureReasonIsLengthCapped(t *testing.T) {
	lines := mcpManagerServerLines([]MCPServerView{
		{Name: "verbose", Transport: "http", State: "failed", Error: strings.Repeat("x", 5000)},
	})
	for _, line := range lines {
		if len(line) > 512 {
			t.Errorf("rendered line is %d bytes, want it capped", len(line))
		}
	}
}
