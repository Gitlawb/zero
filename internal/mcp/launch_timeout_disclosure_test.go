package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/tools"
)

const launchNotice = "denyRead is configured, so the Windows sandbox uses the token shape without WRITE_RESTRICTED (#869)"

func registerWithFactory(t *testing.T, factory func(context.Context, Server) (ToolClient, error)) *Runtime {
	t.Helper()
	runtime, err := RegisterTools(context.Background(), tools.NewRegistry(), config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"slow": {Type: "stdio", Command: "slow-mcp"},
	}}, RegisterOptions{
		ConnectTimeout: 50 * time.Millisecond,
		ClientFactory:  factory,
	})
	if err != nil {
		t.Fatalf("RegisterTools error: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

// A SERVER THAT STARTED AND THEN HUNG STILL RAN UNDER THE REDUCED TOKEN.
//
// Registration abandons a server that exceeds the connect timeout and records it
// as skipped. The startup notices used to leave connectStdio only on the returned
// client or the returned error, and the abandoned attempt returns neither before
// the serial commit phase is over, so the process ran with reduced write
// confinement and startup said only that the server was skipped.
func TestTimeoutAfterLaunchKeepsTheStartupDisclosure(t *testing.T) {
	runtime := registerWithFactory(t, func(ctx context.Context, server Server) (ToolClient, error) {
		// The process started under the reduced token, then initialize hangs.
		publishLaunch(ctx, []string{launchNotice})
		<-ctx.Done()
		return nil, ctx.Err()
	})

	disclosures := runtime.StartupDisclosures()
	if len(disclosures) == 0 {
		t.Fatal("a server that started and then timed out disclosed nothing, so it ran under reduced write confinement unannounced")
	}
	if disclosures[0].Name != "slow" || len(disclosures[0].Notices) != 1 || disclosures[0].Notices[0] != launchNotice {
		t.Errorf("StartupDisclosures() = %#v, want one entry for slow carrying the launch notice", disclosures)
	}
	if skipped := runtime.Skipped(); len(skipped) != 1 || skipped[0].Name != "slow" {
		t.Errorf("the server should still be recorded as skipped: %#v", skipped)
	}
}

// AND A TIMEOUT BEFORE LAUNCH STAYS SILENT.
//
// Without this, retaining the disclosure on timeout could be satisfied by
// disclosing on every timeout, which would claim a token trade for a process
// that was never created.
func TestTimeoutBeforeLaunchDisclosesNothing(t *testing.T) {
	runtime := registerWithFactory(t, func(ctx context.Context, server Server) (ToolClient, error) {
		// Never reached Start: no publish.
		<-ctx.Done()
		return nil, ctx.Err()
	})

	if disclosures := runtime.StartupDisclosures(); len(disclosures) != 0 {
		t.Errorf("a server that never started claimed a token trade: %#v", disclosures)
	}
	if skipped := runtime.Skipped(); len(skipped) != 1 {
		t.Errorf("the server should still be recorded as skipped: %#v", skipped)
	}
}

// THE PUBLISH HAS TO SIT AFTER Start, AND ONLY A REAL LAUNCH PROVES IT.
//
// The two tests above inject a factory, so they exercise the registry's handling
// of the sink and say nothing about where connectStdio publishes to it. Moving
// the call one line up, above cmd.Start, leaves both of them green while every
// failed launch starts claiming the token trade. This one drives the real
// connectStdio with a command that cannot start.
func TestAFailedStartPublishesNoLaunch(t *testing.T) {
	sink := &launchSink{}
	ctx := withLaunchSink(context.Background(), sink)

	client, err := connectStdio(ctx, Server{
		Name:    "missing",
		Type:    "stdio",
		Command: "zero-nonexistent-mcp-binary-for-test",
	}, ConnectOptions{})
	if err == nil {
		if client != nil {
			_ = client.Close()
		}
		t.Fatal("expected a nonexistent executable to fail to start")
	}

	if launched, notices := sink.observe(); launched {
		t.Errorf("a server whose process never started was published as launched (notices %#v)", notices)
	}
}
