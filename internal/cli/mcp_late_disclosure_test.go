package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
)

// A LAUNCH THAT COMPLETES AFTER THE REPORTER HAS RUN MUST STILL BE SAID, ONCE.
//
// Both production paths call reportMCPStartupDisclosures exactly once, right
// after RegisterTools returns. A stdio attempt abandoned at the connect timeout
// can still be inside cmd.Start at that moment; the process then starts under
// the reduced write confinement, the reaper closes its client, and a reporter
// that merely SAMPLED the runtime has already come and gone. The retained sink
// held the fact and nobody read it again, so the operator saw the skipped
// server and never the disclosure.
//
// This drives the REAL reporter against a REAL runtime, rather than polling
// StartupDisclosures by hand, which is what the previous regression did and
// which is precisely how it masked this: a test that re-reads on the tester's
// behalf proves nothing about a production path that does not.
func TestLateMCPLaunchReachesTheStartupReporterExactlyOnce(t *testing.T) {
	const notice = "MCP server started without WRITE_RESTRICTED because denyRead is configured (#869)"
	released := make(chan struct{})

	runtime, err := mcp.RegisterTools(context.Background(), tools.NewRegistry(),
		config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"slow": {Type: "stdio", Command: "slow-mcp"},
		}},
		mcp.RegisterOptions{
			ConnectTimeout: 50 * time.Millisecond,
			ClientFactory: func(ctx context.Context, server mcp.Server) (mcp.ToolClient, error) {
				// Held past the registration timeout AND past the settle grace, so
				// registration has already reaped this attempt and returned.
				<-released
				mcp.PublishLaunchForTest(ctx, []string{notice})
				return nil, errors.New("initialize failed long after start")
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	// The reporter runs ONCE, here, exactly as runExec and the interactive
	// startup path run it: before the launch has resolved.
	var stderr bytes.Buffer
	reportMCPStartupDisclosures(&stderr, runtime)
	if strings.Contains(stderr.String(), notice) {
		t.Fatalf("the disclosure was printed before the process had started:\n%s", stderr.String())
	}

	close(released)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stderr.String(), notice) {
		time.Sleep(5 * time.Millisecond)
	}
	got := stderr.String()
	if n := strings.Count(got, notice); n != 1 {
		t.Fatalf("a launch that completed after the reporter ran was disclosed %d time(s), want exactly 1:\n%s", n, got)
	}
	if !strings.Contains(got, "MCP server slow started with reduced enforcement") {
		t.Errorf("the late disclosure does not name the server:\n%s", got)
	}
	if skipped := runtime.Skipped(); len(skipped) != 1 {
		t.Errorf("the server should still be recorded as skipped: %#v", skipped)
	}
}

// And a server whose launch was already known when the reporter ran is said
// once by it, and not again by the late path.
func TestKnownMCPLaunchIsNotReportedTwice(t *testing.T) {
	const notice = "MCP server started under reduced enforcement"
	runtime, err := mcp.RegisterTools(context.Background(), tools.NewRegistry(),
		config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"fast": {Type: "stdio", Command: "fast-mcp"},
		}},
		mcp.RegisterOptions{
			ConnectTimeout: time.Second,
			ClientFactory: func(ctx context.Context, server mcp.Server) (mcp.ToolClient, error) {
				mcp.PublishLaunchForTest(ctx, []string{notice})
				return nil, errors.New("initialize failed after start")
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	var stderr bytes.Buffer
	reportMCPStartupDisclosures(&stderr, runtime)
	// Give any misrouted late delivery a chance to double up.
	time.Sleep(50 * time.Millisecond)
	if n := strings.Count(stderr.String(), notice); n != 1 {
		t.Fatalf("a launch known at registration was disclosed %d time(s), want exactly 1:\n%s", n, stderr.String())
	}
}
