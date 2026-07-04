package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// probeTool records execution overlap and ordering so tests can assert what
// actually ran concurrently.
type probeTool struct {
	name       string
	sideEffect tools.SideEffect
	delay      time.Duration

	mu        sync.Mutex
	active    int
	maxActive int
	log       []string
}

func (tool *probeTool) Name() string        { return tool.name }
func (tool *probeTool) Description() string { return "test probe tool" }
func (tool *probeTool) Parameters() tools.Schema {
	return tools.Schema{
		Type:                 "object",
		Properties:           map[string]tools.PropertySchema{"id": {Type: "string"}},
		AdditionalProperties: false,
	}
}
func (tool *probeTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tool.sideEffect, Permission: tools.PermissionAllow, Reason: "test"}
}
func (tool *probeTool) Run(_ context.Context, args map[string]any) tools.Result {
	id, _ := args["id"].(string)
	tool.mu.Lock()
	tool.active++
	if tool.active > tool.maxActive {
		tool.maxActive = tool.active
	}
	tool.log = append(tool.log, "start:"+id)
	tool.mu.Unlock()
	time.Sleep(tool.delay)
	tool.mu.Lock()
	tool.active--
	tool.log = append(tool.log, "end:"+id)
	tool.mu.Unlock()
	return tools.Result{Status: tools.StatusOK, Output: "probe " + id}
}

func probeCallEvents(callID, toolName, id string) []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: callID, ToolName: toolName},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: callID, ArgumentsFragment: fmt.Sprintf(`{"id":%q}`, id)},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: callID},
	}
}

func TestParallelSafeToolCall(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&probeTool{name: "probe_read", sideEffect: tools.SideEffectRead})
	registry.Register(&probeTool{name: "probe_write", sideEffect: tools.SideEffectWrite})

	call := func(name, args string) ToolCall { return ToolCall{ID: "c", Name: name, Arguments: args} }
	if !parallelSafeToolCall(registry, call("probe_read", `{"id":"a"}`), Options{}) {
		t.Fatal("auto-allowed read tool must be parallel-safe")
	}
	if parallelSafeToolCall(registry, call("probe_write", `{"id":"a"}`), Options{}) {
		t.Fatal("mutating tool must not be parallel-safe")
	}
	if parallelSafeToolCall(registry, call("unknown_tool", `{}`), Options{}) {
		t.Fatal("unknown tool must not be parallel-safe")
	}
	if parallelSafeToolCall(registry, call("probe_read", `{"id":`), Options{}) {
		t.Fatal("undecodable arguments must not be parallel-safe")
	}
	if parallelSafeToolCall(registry, call("ask_user", `{}`), Options{}) {
		t.Fatal("loop-intercepted tools must stay sequential")
	}
}

func TestRunExecutesConsecutiveReadsConcurrently(t *testing.T) {
	probe := &probeTool{name: "probe_read", sideEffect: tools.SideEffectRead, delay: 60 * time.Millisecond}
	registry := tools.NewRegistry()
	registry.Register(probe)

	turnOne := append(probeCallEvents("call-1", "probe_read", "a"), probeCallEvents("call-2", "probe_read", "b")...)
	turnOne = append(turnOne, probeCallEvents("call-3", "probe_read", "c")...)
	turnOne = append(turnOne, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone})
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			turnOne,
			{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}},
		},
	}

	var results []ToolResult
	_, err := Run(context.Background(), "probe", provider, Options{
		Registry:     registry,
		OnToolResult: func(result ToolResult) { results = append(results, result) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if probe.maxActive < 2 {
		t.Fatalf("consecutive read-only calls must overlap, max concurrency was %d", probe.maxActive)
	}
	// Results must still be recorded in original call order.
	if len(results) != 3 || results[0].ToolCallID != "call-1" || results[1].ToolCallID != "call-2" || results[2].ToolCallID != "call-3" {
		t.Fatalf("tool results out of order: %#v", results)
	}
	messages := provider.requests[1].Messages
	var toolOrder []string
	for _, message := range messages {
		if message.Role == zeroruntime.MessageRoleTool {
			toolOrder = append(toolOrder, message.ToolCallID)
		}
	}
	if len(toolOrder) != 3 || toolOrder[0] != "call-1" || toolOrder[1] != "call-2" || toolOrder[2] != "call-3" {
		t.Fatalf("recorded tool messages out of order: %v", toolOrder)
	}
}

func TestRunParallelReadsNeverSpanMutatingCall(t *testing.T) {
	read := &probeTool{name: "probe_read", sideEffect: tools.SideEffectRead, delay: 30 * time.Millisecond}
	write := &probeTool{name: "probe_write", sideEffect: tools.SideEffectWrite}
	// Shared log across both probes so relative ordering is observable.
	write.mu = sync.Mutex{}
	registry := tools.NewRegistry()
	registry.Register(read)
	registry.Register(write)

	turnOne := append(probeCallEvents("call-1", "probe_read", "r1"), probeCallEvents("call-2", "probe_read", "r2")...)
	turnOne = append(turnOne, probeCallEvents("call-3", "probe_write", "w")...)
	turnOne = append(turnOne, probeCallEvents("call-4", "probe_read", "r3")...)
	turnOne = append(turnOne, probeCallEvents("call-5", "probe_read", "r4")...)
	turnOne = append(turnOne, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone})
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			turnOne,
			{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}},
		},
	}

	_, err := Run(context.Background(), "probe", provider, Options{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	// The write must start only after r1+r2 finished, and r3/r4 must start only
	// after the write finished: batches never cross a mutating call.
	index := func(log []string, entry string) int {
		for i, e := range log {
			if e == entry {
				return i
			}
		}
		return -1
	}
	readLog := func() []string { read.mu.Lock(); defer read.mu.Unlock(); return append([]string(nil), read.log...) }()
	writeLog := func() []string { write.mu.Lock(); defer write.mu.Unlock(); return append([]string(nil), write.log...) }()
	if index(writeLog, "start:w") == -1 {
		t.Fatalf("write probe never ran: %v", writeLog)
	}
	for _, entry := range []string{"end:r1", "end:r2"} {
		if index(readLog, entry) == -1 {
			t.Fatalf("first read batch incomplete: %v", readLog)
		}
	}
	// r3/r4 must appear strictly after r1/r2 in the read log (the write barrier
	// between the two batches forces full separation).
	firstBatchMaxEnd := max(index(readLog, "end:r1"), index(readLog, "end:r2"))
	secondBatchMinStart := min(index(readLog, "start:r3"), index(readLog, "start:r4"))
	if secondBatchMinStart < firstBatchMaxEnd {
		t.Fatalf("second read batch started before first batch ended: %v", readLog)
	}
	if read.maxActive < 2 {
		t.Fatalf("reads within a batch must overlap, max concurrency was %d", read.maxActive)
	}
}
