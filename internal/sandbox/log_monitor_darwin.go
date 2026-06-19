//go:build darwin

package sandbox

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"sync"
)

// sandboxLogRingSize caps how many distinct denial lines a monitor retains, so a
// chatty run cannot grow memory without bound.
const sandboxLogRingSize = 64

// sandboxLogNoise lists daemons whose denials are background chatter, not caused
// by the agent's command; they are filtered out of the reported blocks.
var sandboxLogNoise = []string{
	"mDNSResponder",
	"com.apple.diagnosticd",
	"com.apple.analyticsd",
}

// DenialMonitor tails `log stream` for seatbelt denials carrying a unique tag and
// collects them so a caller can report what the sandbox blocked. It is
// best-effort: if `log` cannot start it yields nothing and the command is
// unaffected.
//
// NOTE: on some macOS versions/environments seatbelt denials are not delivered to
// the unified log queryable by `log stream` (verified empty on at least one host),
// in which case this captures nothing. It never affects whether the command runs.
type DenialMonitor struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	blocks []string
}

// StartDenialMonitor begins streaming sandbox denials tagged with tag. An empty
// tag (monitoring disabled) yields a no-op monitor.
func StartDenialMonitor(ctx context.Context, tag string) *DenialMonitor {
	monitor := &DenialMonitor{done: make(chan struct{})}
	if strings.TrimSpace(tag) == "" {
		close(monitor.done)
		return monitor
	}
	streamCtx, cancel := context.WithCancel(ctx)
	monitor.cancel = cancel
	monitor.cmd = exec.CommandContext(streamCtx, "log", "stream", "--style", "compact",
		"--predicate", `eventMessage CONTAINS "`+tag+`"`)
	stdout, err := monitor.cmd.StdoutPipe()
	if err != nil {
		cancel()
		close(monitor.done)
		return monitor
	}
	if err := monitor.cmd.Start(); err != nil {
		cancel()
		close(monitor.done)
		return monitor
	}
	go func() {
		defer close(monitor.done)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			if msg, ok := parseSandboxDenyLine(scanner.Text(), tag); ok {
				monitor.record(msg)
			}
		}
		// Reap the `log stream` child once stdout is drained; without Wait the
		// cancelled process lingers as a zombie across repeated runs. Safe here —
		// all reads from the pipe are complete.
		_ = monitor.cmd.Wait()
	}()
	return monitor
}

func (monitor *DenialMonitor) record(msg string) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	for _, existing := range monitor.blocks {
		if existing == msg {
			return // dedupe repeats of the same denial
		}
	}
	monitor.blocks = append(monitor.blocks, msg)
	if len(monitor.blocks) > sandboxLogRingSize {
		monitor.blocks = monitor.blocks[len(monitor.blocks)-sandboxLogRingSize:]
	}
}

// Stop ends the stream and returns the collected denial messages (possibly empty).
func (monitor *DenialMonitor) Stop() []string {
	if monitor.cancel != nil {
		monitor.cancel()
	}
	<-monitor.done
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	return append([]string(nil), monitor.blocks...)
}

// parseSandboxDenyLine extracts a human-readable denial from a `log stream` line
// that is a Sandbox deny carrying tag. It returns ("", false) for non-denials,
// background-noise daemons, or lines missing the tag.
func parseSandboxDenyLine(line string, tag string) (string, bool) {
	if tag != "" && !strings.Contains(line, tag) {
		return "", false
	}
	if !strings.Contains(line, "Sandbox:") || !strings.Contains(line, "deny") {
		return "", false
	}
	for _, noisy := range sandboxLogNoise {
		if strings.Contains(line, noisy) {
			return "", false
		}
	}
	message := line
	if idx := strings.Index(line, "Sandbox:"); idx >= 0 {
		message = line[idx:]
	}
	if tag != "" {
		message = strings.ReplaceAll(message, tag, "")
	}
	return strings.Join(strings.Fields(message), " "), true
}
