package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const providerCommandTimeout = 5 * time.Second

func LoadProviderCommand(command string) (FileConfig, error) {
	stdout, stderr, err := runProviderCommand(command, providerCommandTimeout)
	if err != nil {
		if errors.Is(err, errProviderCommandTimeout) {
			// Report which phase ran out of time, and anything the command
			// managed to say. This used to be a bare string with the underlying
			// error and stderr both dropped, which left a user whose command was
			// slow on a cold machine with nothing to act on.
			return FileConfig{}, fmt.Errorf("provider command timed out after %s: %w%s", providerCommandTimeout, err, commandOutput(stderr))
		}
		return FileConfig{}, fmt.Errorf("provider command failed: %w%s", err, commandOutput(stderr))
	}

	cfg, err := parseProviderCommandJSON(stdout)
	if err != nil {
		return FileConfig{}, err
	}
	if len(cfg.Providers) == 0 {
		return FileConfig{}, fmt.Errorf("provider command returned no providers")
	}

	providers, _, err := normalizeProvidersWithoutModelDefaults(cfg.Providers, cfg.ActiveProvider, map[string]string{})
	if err != nil {
		return FileConfig{}, err
	}
	cfg.Providers = providers
	return cfg, nil
}

var errProviderCommandTimeout = errors.New("provider command timeout")

// providerCommandDrainGrace bounds how long the timeout path waits for cmd.Wait
// to return after the process tree has been terminated. Terminate normally makes
// that immediate; this exists so a descendant that refuses to die cannot extend
// a call whose deadline has already expired.
const providerCommandDrainGrace = 2 * time.Second

// syncBuffer serialises the command's I/O pump against reads from this package.
// On the timeout path runProviderCommand can return while cmd.Wait is still
// draining, so the buffers outlive the call and a plain bytes.Buffer would be
// read and written concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// snapshot copies out what has been written so far. A copy rather than the
// buffer's own slice, which the pump may still append to.
func (b *syncBuffer) snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// awaitWithGrace waits for done and gives up after grace, reporting whether the
// wait completed rather than expired.
//
// Split out from the timeout path so the bound can be asserted on its own. In
// place it is effectively untestable: Terminate kills the tree, so Wait returns
// promptly and an unbounded receive would behave identically in every test that
// is not pathological, which is exactly how the old unbounded receive survived.
func awaitWithGrace(done <-chan error, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// runProviderCommand runs a provider command under a real deadline.
//
// "Real" is the point: timeout used to bound only the wait, so the call could
// take arbitrarily longer than it. Two phases sat outside it. Starting the
// process ran before the clock began, and on Windows that is CREATE_SUSPENDED,
// job-object creation and assignment, and a system-wide thread snapshot, none of
// which is fast on a cold or contended machine. Draining after termination was
// unbounded as well. Both are inside the budget now, so the deadline is an upper
// bound rather than a floor.
func runProviderCommand(command string, timeout time.Duration) ([]byte, []byte, error) {
	cmd := shellCommand(command)
	// Bound the time Wait spends draining I/O pipes after the process exits,
	// in case an orphaned descendant still holds the write ends open.
	cmd.WaitDelay = time.Second

	stdout := &syncBuffer{}
	stderr := &syncBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Armed before the process is started, so process creation is inside the
	// budget rather than ahead of it.
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	type startResult struct {
		proc *commandProcess
		err  error
	}
	// Buffered, so the goroutine can finish and exit even when nobody is left
	// waiting on it because the deadline already fired.
	started := make(chan startResult, 1)
	go func() {
		proc, err := startCommandProcess(cmd)
		started <- startResult{proc: proc, err: err}
	}()

	var proc *commandProcess
	select {
	case result := <-started:
		if result.err != nil {
			return stdout.snapshot(), stderr.snapshot(), result.err
		}
		proc = result.proc
	case <-deadline.C:
		// The start is still in flight and will eventually hand back whatever it
		// created, so cleanup is handed off rather than abandoned. Wait reaps the
		// process; without it a started-but-orphaned command becomes a zombie.
		go func() {
			result := <-started
			if result.err != nil {
				return
			}
			result.proc.Terminate()
			_ = cmd.Wait()
			result.proc.Close()
		}()
		return stdout.snapshot(), stderr.snapshot(), fmt.Errorf("%w: starting the command exceeded %s", errProviderCommandTimeout, timeout)
	}
	defer proc.Close()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			// The shell exited (whether via ErrWaitDelay because a
			// background descendant kept the inherited stdout/stderr pipes
			// open, or via a nonzero exit status) but a leftover descendant
			// may still be running. Terminate is a no-op against an
			// already-dead tree, so always call it rather than gating on
			// the specific error to avoid leaking that descendant.
			proc.Terminate()
		}
		if errors.Is(err, exec.ErrWaitDelay) {
			// Wrap rather than replace. A descendant holding the inherited pipes
			// open and the command never finishing at all are different failures
			// that both collapsed into the same bare sentinel, which left callers
			// and tests unable to tell which one happened. Callers that only ask
			// errors.Is(err, errProviderCommandTimeout) are unaffected, so the
			// message LoadProviderCommand reports is unchanged.
			return stdout.snapshot(), stderr.snapshot(), fmt.Errorf("%w: %w", errProviderCommandTimeout, err)
		}
		return stdout.snapshot(), stderr.snapshot(), err
	case <-deadline.C:
		proc.Terminate()
		// Bounded, unlike the bare receive this replaces. Terminate kills the
		// tree so Wait almost always returns at once, but "almost always" was
		// doing real work here: a tree that resists termination used to hold the
		// call open indefinitely, long past the deadline that had just expired.
		// Returning early leaves cmd.Wait running, which is why the buffers are
		// synchronised.
		awaitWithGrace(done, providerCommandDrainGrace)
		return stdout.snapshot(), stderr.snapshot(), fmt.Errorf("%w: the command did not finish within %s", errProviderCommandTimeout, timeout)
	}
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(strings.TrimSpace(command), `"`) {
			command = "call " + command
		}
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

func commandOutput(stderr []byte) string {
	output := strings.TrimSpace(string(stderr))
	if output == "" {
		return ""
	}
	return ": " + redactSecrets(output)
}

func parseProviderCommandJSON(data []byte) (FileConfig, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return FileConfig{}, fmt.Errorf("provider command returned empty JSON")
	}

	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err == nil && (len(cfg.Providers) > 0 || cfg.ActiveProvider != "" || cfg.MaxTurns > 0) {
		if cfg.ActiveProvider == "" && len(cfg.Providers) == 1 {
			cfg.ActiveProvider = cfg.Providers[0].Name
		}
		return cfg, nil
	}

	var profile ProviderProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return FileConfig{}, fmt.Errorf("invalid provider command JSON: %w", err)
	}
	if profile.Name == "" {
		profile.Name = string(ProviderKindOpenAI)
	}
	return FileConfig{
		ActiveProvider: profile.Name,
		Providers:      []ProviderProfile{profile},
	}, nil
}
