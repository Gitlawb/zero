package mcp

import (
	"context"
	"sync"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/tools"
)

// disclosingRuntime registers one server whose launch carried a startup notice,
// so its runtime has something the stream could deliver.
func disclosingRuntime(t *testing.T) *Runtime {
	t.Helper()
	client := &disclosingFakeClient{
		fakeToolClient: fakeToolClient{listed: []RemoteTool{{Name: "lookup", Description: "Lookup"}}},
		notices:        []string{startupNotice},
	}
	runtime, err := RegisterTools(context.Background(), tools.NewRegistry(), config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp"},
	}}, RegisterOptions{ClientFactory: func(context.Context, Server) (ToolClient, error) { return client, nil }})
	if err != nil {
		t.Fatalf("RegisterTools() error = %v", err)
	}
	if disclosures := runtime.StartupDisclosures(); len(disclosures) != 1 {
		t.Fatalf("SETUP INVALID: the runtime has %d disclosures, so an empty stream would prove nothing", len(disclosures))
	}
	return runtime
}

// A CLOSED RUNTIME HANDS OUT A CLOSED STREAM.
//
// Close read the stream field directly. Before anyone had asked for the stream
// that field was nil, so Close closed nothing, and the next caller built a fresh,
// open stream, queued this runtime's disclosures into it and subscribed it to
// launches from a runtime that was already gone. A pump started on it then
// waited forever.
func TestAStreamRequestedAfterCloseIsAlreadyClosed(t *testing.T) {
	runtime := disclosingRuntime(t)
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	stream := runtime.StartupDisclosureStream()
	if stream == nil {
		t.Fatal("StartupDisclosureStream() returned nil for a runtime")
	}
	if queued := stream.Drain(); len(queued) != 0 {
		t.Errorf("a stream requested after Close delivered %#v from a runtime that is gone", queued)
	}
	if stream.Wait() {
		t.Error("the stream is still open after Close, so a pump started on it never returns")
	}
}

// And the stream an owner already holds is the one Close ends, with what was
// queued before Close still drainable.
func TestCloseEndsTheStreamAnOwnerAlreadyHolds(t *testing.T) {
	runtime := disclosingRuntime(t)
	stream := runtime.StartupDisclosureStream()
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if again := runtime.StartupDisclosureStream(); again != stream {
		t.Error("Close replaced the stream the owner holds, so the owner's pump is never told to stop")
	}
	if queued := stream.Drain(); len(queued) != 1 {
		t.Errorf("the disclosure queued before Close was lost: %#v", queued)
	}
	if stream.Wait() {
		t.Error("Close did not end the stream the owner holds")
	}
}

// NO RACE BETWEEN CLOSE AND THE FIRST REQUEST FOR THE STREAM.
//
// Close read the field while a first StartupDisclosureStream call could be
// writing it inside the Once on another goroutine. Only meaningful under -race,
// which the race job runs.
func TestCloseAndTheFirstStreamRequestDoNotRace(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		runtime := disclosingRuntime(t)
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			_ = runtime.StartupDisclosureStream()
		}()
		go func() {
			defer group.Done()
			<-start
			_ = runtime.Close()
		}()
		close(start)
		group.Wait()
		// Whichever call won the Once, the stream is closed now. A disclosure
		// queued before Close stays drainable, so drain before asking.
		stream := runtime.StartupDisclosureStream()
		stream.Drain()
		if stream.Wait() {
			t.Fatal("the stream survived Close")
		}
	}
}
