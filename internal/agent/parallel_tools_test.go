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
	// shared, when set, receives this probe's start/end entries too, so a test
	// can observe ordering ACROSS probes (e.g. reads vs a write barrier).
	shared *probeLog

	mu        sync.Mutex
	active    int
	maxActive int
	log       []string
}

// probeLog is a mutex-guarded event log shared between probes.
type probeLog struct {
	mu      sync.Mutex
	entries []string
}

func (log *probeLog) append(entry string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.entries = append(log.entries, entry)
}

func (log *probeLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.entries...)
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
	if tool.shared != nil {
		tool.shared.append("start:" + id)
	}
	time.Sleep(tool.delay)
	if tool.shared != nil {
		tool.shared.append("end:" + id)
	}
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
	shared := &probeLog{}
	read := &probeTool{name: "probe_read", sideEffect: tools.SideEffectRead, delay: 30 * time.Millisecond, shared: shared}
	write := &probeTool{name: "probe_write", sideEffect: tools.SideEffectWrite, shared: shared}
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

	// Cross-probe ordering on the SHARED log: the write must start only after
	// both first-batch reads finished, and both second-batch reads must start
	// only after the write finished — batches never cross a mutating call.
	log := shared.snapshot()
	index := func(entry string) int {
		for i, e := range log {
			if e == entry {
				return i
			}
		}
		t.Fatalf("entry %q missing from shared log: %v", entry, log)
		return -1
	}
	writeStart, writeEnd := index("start:w"), index("end:w")
	if firstBatchMaxEnd := max(index("end:r1"), index("end:r2")); writeStart < firstBatchMaxEnd {
		t.Fatalf("write started before the first read batch finished: %v", log)
	}
	if secondBatchMinStart := min(index("start:r3"), index("start:r4")); secondBatchMinStart < writeEnd {
		t.Fatalf("second read batch started before the write finished: %v", log)
	}
	if read.maxActive < 2 {
		t.Fatalf("reads within a batch must overlap, max concurrency was %d", read.maxActive)
	}
}
