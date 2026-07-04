package agent

import (
	"context"
	"sync"

	"github.com/Gitlawb/zero/internal/tools"
)

// Parallel read-ahead for tool batches. When a turn requests several
// independent lookups (read_file + grep + glob is the common shape), executing
// them one after another serializes pure I/O waits. A consecutive run of
// auto-allowed read-only calls is executed concurrently instead; results are
// then consumed in the original call order, so guard counters, message
// ordering, abort semantics, and the surface's call/result event pairing are
// byte-identical to sequential execution. Runs never span a mutating call: a
// read that follows a write must observe the write, so eligibility is decided
// per consecutive run, not per batch.

// maxParallelReadTools bounds concurrent read-only tool executions in a turn.
const maxParallelReadTools = 8

// precomputedToolResult is one parallel read-ahead execution, keyed back to
// its batch index by the caller.
type precomputedToolResult struct {
	result   ToolResult
	abortErr error
}

// parallelSafeToolCall reports whether call may run concurrently with its
// neighbors: the tool must exist, be side-effect-free (SideEffectRead), and be
// auto-allowed for these args, so no interactive prompt or workspace mutation
// is on the hot path. Loop-intercepted tools (ask_user, request_permissions)
// and tool_search (mutates the deferred-tool set) stay sequential.
func parallelSafeToolCall(registry *tools.Registry, call ToolCall, options Options) bool {
	switch call.Name {
	case "ask_user", tools.RequestPermissionsToolName, tools.ToolSearchToolName:
		return false
	}
	tool, found := registry.Get(call.Name)
	if !found || tool.Safety().SideEffect != tools.SideEffectRead {
		return false
	}
	args := map[string]any{}
	if call.Arguments != "" {
		if err := decodeToolArguments(call.Arguments, &args); err != nil {
			return false
		}
	}
	return effectivePermission(tool, args) == tools.PermissionAllow
}

// executeParallelReadBatch runs calls[start:end] concurrently (bounded by
// maxParallelReadTools) and returns results indexed relative to start. Any
// permission prompt that still occurs (a sandbox preflight can demand one even
// for an auto-allowed read) is serialized so an interactive front-end never
// sees two prompts at once.
func executeParallelReadBatch(ctx context.Context, registry *tools.Registry, calls []ToolCall, start, end int, permissionMode PermissionMode, options Options) []precomputedToolResult {
	batchOptions := options
	if options.OnPermissionRequest != nil {
		var promptMutex sync.Mutex
		inner := options.OnPermissionRequest
		batchOptions.OnPermissionRequest = func(ctx context.Context, request PermissionRequest) (PermissionDecision, error) {
			promptMutex.Lock()
			defer promptMutex.Unlock()
			return inner(ctx, request)
		}
	}

	results := make([]precomputedToolResult, end-start)
	semaphore := make(chan struct{}, maxParallelReadTools)
	var waitGroup sync.WaitGroup
	for index := start; index < end; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			result, abortErr := executeToolCall(ctx, registry, calls[index], permissionMode, batchOptions)
			results[index-start] = precomputedToolResult{result: result, abortErr: abortErr}
		}(index)
	}
	waitGroup.Wait()
	return results
}
