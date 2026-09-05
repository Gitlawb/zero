package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/execution"
	"github.com/Gitlawb/zero/internal/tools"
)

const (
	adapterHelperModeEnv   = "ZERO_TEST_MCP_ADAPTER_MODE"
	adapterHelperReportEnv = "ZERO_TEST_MCP_ADAPTER_REPORT"

	// answerThenPublish models the ordering the fix is about: the child is
	// serving before the adapter has recorded that it exists.
	answerThenPublish = "answer-then-publish"
	// failThenPublish is the same ordering on the other exit: the handshake dies
	// first and the adapter publishes on its way out.
	failThenPublish = "fail-then-publish"
	// failAndNeverPublish is the companion that keeps the two above from being
	// satisfied by disclosing unconditionally.
	failAndNeverPublish = "fail-and-never-publish"
)

// TestAdapterHelperProcess is the helper process body, not a test.
//
// It exists so the report can be published at a controlled point RELATIVE TO THE
// HANDSHAKE. A fixture that writes the finished report during PrepareExecution,
// as the older test does, publishes before the helper command has even started,
// so every ordering this file is about is already over before the parent looks.
func TestAdapterHelperProcess(t *testing.T) {
	mode := os.Getenv(adapterHelperModeEnv)
	if mode == "" {
		t.Skip("not the helper process")
	}
	reportPath := os.Getenv(adapterHelperReportEnv)
	publish := func() {
		// Same shape the Windows helper writes, into the file the preparer already
		// created empty.
		_ = os.WriteFile(reportPath, []byte(`{"childLaunched":true}`), 0o600)
	}

	switch mode {
	case answerThenPublish:
		serveOneMCPSessionThenPublish(publish)
	case failThenPublish:
		// The handshake dies here, before anything is recorded. Closing stdout is
		// the child going away; the adapter is still running.
		_ = os.Stdout.Close()
		time.Sleep(300 * time.Millisecond)
		publish()
	case failAndNeverPublish:
		_ = os.Stdout.Close()
		time.Sleep(300 * time.Millisecond)
	}
	// Before the framework can write anything to the pipe the parent is reading.
	os.Exit(0)
}

// serveOneMCPSessionThenPublish answers the handshake and only then records that
// the child exists, which is the interleaving that used to lose the disclosure.
func serveOneMCPSessionThenPublish(publish func()) {
	reader := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	published := false
	for {
		line, err := reader.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			var message struct {
				ID     *int   `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &message) == nil && message.ID != nil {
				var result string
				switch message.Method {
				case "initialize":
					result = `{"protocolVersion":"2024-11-05"}`
				case "tools/list":
					result = `{"tools":[]}`
				default:
					result = `{}`
				}
				fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", *message.ID, result)
				_ = out.Flush()
				if message.Method == "initialize" && !published {
					// AFTER the response is on the wire. The parent can act on a
					// handshake that succeeded before this line runs.
					time.Sleep(300 * time.Millisecond)
					publish()
					published = true
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// livePublishPreparer models the Windows wrapped plan with the real publication
// ordering: the report file is created EMPTY before the launch, exactly as
// openWindowsExecutionReport does, and the helper fills it in later.
type livePublishPreparer struct {
	mode       string
	reportPath string
	called     bool
}

func (preparer *livePublishPreparer) PrepareExecution(_ context.Context, _ execution.Request) (execution.PreparedCommand, error) {
	preparer.called = true
	// The empty file the parent can see before anything is published. This is the
	// state that is neither "no child" nor "child created".
	if err := os.WriteFile(preparer.reportPath, nil, 0o600); err != nil {
		return execution.PreparedCommand{}, err
	}
	command := exec.Command(os.Args[0], "-test.run=^TestAdapterHelperProcess$")
	command.Env = append(os.Environ(),
		adapterHelperModeEnv+"="+preparer.mode,
		adapterHelperReportEnv+"="+preparer.reportPath,
	)
	path := preparer.reportPath
	return execution.PreparedCommand{
		Command:                   command,
		ChildLaunchOwnedByAdapter: true,
		Enforcement:               execution.Enforcement{Notices: []string{adapterLaunchNotice}},
		Report: func() (execution.AdapterReport, error) {
			raw, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				return execution.AdapterReport{}, nil
			}
			if err != nil {
				return execution.AdapterReport{}, err
			}
			var report execution.AdapterReport
			// An empty or half-written file decodes to an error, which is the
			// unsettled state and not an answer.
			if err := json.Unmarshal(raw, &report); err != nil {
				return execution.AdapterReport{}, err
			}
			return report, nil
		},
		Cleanup: func() { _ = os.Remove(path) },
	}, nil
}

func registerWithLivePublisher(t *testing.T, mode string) (*Runtime, *livePublishPreparer) {
	t.Helper()
	preparer := &livePublishPreparer{mode: mode, reportPath: filepath.Join(t.TempDir(), "report.json")}
	runtime, err := RegisterTools(context.Background(), tools.NewRegistry(), config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: helperCommandName()},
	}}, RegisterOptions{
		Execution:     execution.NewRunner(preparer),
		WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if !preparer.called {
		t.Fatal("SETUP INVALID: the preparer never ran, so connectStdio was never reached")
	}
	return runtime, preparer
}

// A HANDSHAKE THAT SUCCEEDED IS PROOF THE CHILD RAN, WHATEVER THE REPORT SAYS YET.
//
// The child is created before the adapter records it, and it inherits the MCP
// pipes, so it can answer initialize while the report file is still the empty one
// the adapter opened. Reading it at that instant and remembering the answer
// turned "not yet" into "never" for the rest of the session, and the operator was
// never told the server serving these tools ran without the write jail.
//
// The adapter speaks no MCP, so a well-formed response can only have come from
// the requested child. That is terminal evidence and needs no report.
func TestADisclosureSurvivesAReportPublishedAfterTheHandshake(t *testing.T) {
	runtime, _ := registerWithLivePublisher(t, answerThenPublish)

	// SETUP: the server really connected, or this is the failure path instead.
	if skipped := runtime.Skipped(); len(skipped) != 0 {
		t.Fatalf("SETUP INVALID: the server did not connect (%v), so the successful-handshake path is not under test", skipped)
	}
	disclosures := runtime.StartupDisclosures()
	if len(disclosures) != 1 {
		t.Fatalf("a server that answered the handshake produced %d disclosures, want exactly one: %v", len(disclosures), disclosures)
	}
	if len(disclosures[0].Notices) != 1 || disclosures[0].Notices[0] != adapterLaunchNotice {
		t.Fatalf("the disclosure carries %v, want the one planned notice", disclosures[0].Notices)
	}
}

// AND ON THE OTHER EXIT, THE DECISION WAITS FOR THE ADAPTER.
//
// Here the handshake dies first and the adapter publishes on its way out. The
// decision used to be taken before Close, precisely because Close deletes the
// report, so it read the empty file and answered "no child" about a server that
// really had run. Settling from inside cleanup, with the file still there, is what
// lets the answer be taken after the adapter is terminal instead of before it.
func TestADisclosureSurvivesAReportPublishedAfterAFailedHandshake(t *testing.T) {
	runtime, _ := registerWithLivePublisher(t, failThenPublish)

	// SETUP: the attempt really failed, or the successful path is being measured.
	if len(runtime.Skipped()) == 0 {
		t.Fatal("SETUP INVALID: the server connected, so the initialize-failure path is not under test")
	}
	disclosures := runtime.StartupDisclosures()
	if len(disclosures) != 1 {
		t.Fatalf("a helper that published its launch on the way out produced %d disclosures, want exactly one: %v", len(disclosures), disclosures)
	}
}

// AND AN ADAPTER THAT NEVER PUBLISHED STILL DISCLOSES NOTHING.
//
// The companion that keeps both cases above from being satisfied by announcing
// unconditionally. Settling reads the report one last time and finds nothing,
// which after the adapter has exited is the answer rather than a race.
func TestNoDisclosureWhenTheAdapterExitsWithoutPublishing(t *testing.T) {
	runtime, _ := registerWithLivePublisher(t, failAndNeverPublish)

	if len(runtime.Skipped()) == 0 {
		t.Fatal("SETUP INVALID: the server connected, so the initialize-failure path is not under test")
	}
	if got := runtime.StartupDisclosures(); len(got) != 0 {
		t.Fatalf("a helper that published nothing announced %v; no server is known to have run", got)
	}
}
