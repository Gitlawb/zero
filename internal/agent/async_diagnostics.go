package agent

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Post-edit LSP diagnostics used to run synchronously inside edit_file /
// write_file: every edit blocked its tool result on a fresh-publish wait with
// a quiet-period debounce (≥300ms floor that reset on each publish, 10s cap) —
// the largest recurring latency tax in an edit-heavy session. Collection is
// now asynchronous: a file is enqueued the moment a mutating tool finishes, a
// single worker checks it while the rest of the tool batch (and any
// self-correct verification) runs, and the loop drains completed results just
// before building the NEXT provider request, appending errors as a user-role
// nudge. The model always issues another request after an edit turn — it has
// to read its tool results — so it still sees introduced errors at the same
// decision point as before, without any tool call stalling on the language
// server.
//
// A file re-edited before its check runs is checked once in its final state
// (the checker re-reads the file); a file re-edited after its check completed
// is re-enqueued and the newer result wins. When the worker is still busy at
// drain time, the drain gives up after a short wait and the results are
// delivered on the following turn instead — they stay accurate because any
// further edit re-enqueues the file.

// asyncDiagnosticsDrainTimeout bounds how long a turn waits on the in-flight
// check before deferring delivery to the next turn. Most checks finish well
// under it (debounce floor + analysis); a cold language server on a large
// repo does not get to stall the loop the way the old inline 10s cap could.
// A var so tests can shorten the wait.
var asyncDiagnosticsDrainTimeout = 3 * time.Second

// asyncDiagnosticsFinalDrainTimeout is the wait used when the run is about to
// FINALIZE (natural completion or the max-turns summary): there is no later
// turn to defer to, so the gate waits out the old inline-era budget rather
// than silently dropping an error the last edit introduced. A var so tests
// can shorten the wait.
var asyncDiagnosticsFinalDrainTimeout = 10 * time.Second

// asyncDiagnosticsNudge prefixes the drained diagnostics blocks; phrased like
// the old inline block so the model reacts the same way.
const asyncDiagnosticsNudge = "Diagnostics after your recent edits (fix any errors you introduced):\n"

type asyncDiagnostics struct {
	check         func(context.Context, string) string
	workspaceRoot string

	mu      sync.Mutex
	queue   []string
	queued  map[string]bool
	results map[string]string
	// working is non-nil while the worker goroutine runs and is closed when it
	// exits, so drain can wait for quiescence without polling.
	working chan struct{}
}

// newAsyncDiagnostics returns nil when check is nil, and every method on a nil
// receiver is a no-op — callers wire it unconditionally.
func newAsyncDiagnostics(check func(context.Context, string) string, workspaceRoot string) *asyncDiagnostics {
	if check == nil {
		return nil
	}
	return &asyncDiagnostics{
		check:         check,
		workspaceRoot: workspaceRoot,
		queued:        map[string]bool{},
		results:       map[string]string{},
	}
}

// enqueue schedules changed files for a background check. Paths are
// workspace-relative from Result.ChangedFiles (absolute when under a granted
// extra write root), mirroring SelfCorrector's resolution.
func (diagnostics *asyncDiagnostics) enqueue(ctx context.Context, files []string) {
	if diagnostics == nil || len(files) == 0 {
		return
	}
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	for _, file := range files {
		path := diagnostics.absPath(file)
		if diagnostics.queued[path] {
			continue
		}
		diagnostics.queued[path] = true
		// A completed result for this file describes a pre-edit state now.
		delete(diagnostics.results, path)
		diagnostics.queue = append(diagnostics.queue, path)
	}
	if diagnostics.working == nil && len(diagnostics.queue) > 0 {
		done := make(chan struct{})
		diagnostics.working = done
		go diagnostics.work(ctx, done)
	}
}

func (diagnostics *asyncDiagnostics) work(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		diagnostics.mu.Lock()
		if len(diagnostics.queue) == 0 || ctx.Err() != nil {
			diagnostics.working = nil
			diagnostics.mu.Unlock()
			return
		}
		path := diagnostics.queue[0]
		diagnostics.queue = diagnostics.queue[1:]
		// Un-mark before checking so an edit that lands DURING the check
		// re-enqueues the file and it is checked again in its final state.
		delete(diagnostics.queued, path)
		diagnostics.mu.Unlock()

		block := diagnostics.check(ctx, path)

		diagnostics.mu.Lock()
		if block != "" {
			diagnostics.results[path] = block
		} else {
			delete(diagnostics.results, path)
		}
		diagnostics.mu.Unlock()
	}
}

// drain waits briefly for the worker to go quiet, then formats and consumes
// all completed error blocks as one nudge. Returns "" when there is nothing
// to report or the worker is still busy (results then surface next turn).
func (diagnostics *asyncDiagnostics) drain(ctx context.Context) string {
	return diagnostics.drainWithin(ctx, asyncDiagnosticsDrainTimeout)
}

// drainFinal is drain with the finalization budget: called before the run's
// last model request, where an undelivered result would otherwise be lost.
func (diagnostics *asyncDiagnostics) drainFinal(ctx context.Context) string {
	return diagnostics.drainWithin(ctx, asyncDiagnosticsFinalDrainTimeout)
}

func (diagnostics *asyncDiagnostics) drainWithin(ctx context.Context, timeout time.Duration) string {
	if diagnostics == nil {
		return ""
	}
	diagnostics.mu.Lock()
	busy := diagnostics.working
	diagnostics.mu.Unlock()
	if busy != nil {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-busy:
		case <-timer.C:
			return ""
		case <-ctx.Done():
			return ""
		}
	}
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	if len(diagnostics.results) == 0 {
		return ""
	}
	paths := make([]string, 0, len(diagnostics.results))
	for path := range diagnostics.results {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	blocks := make([]string, 0, len(paths))
	for _, path := range paths {
		blocks = append(blocks, diagnostics.results[path])
	}
	diagnostics.results = map[string]string{}
	return asyncDiagnosticsNudge + strings.Join(blocks, "\n")
}

func (diagnostics *asyncDiagnostics) absPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(diagnostics.workspaceRoot, path)
}
