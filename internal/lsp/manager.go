package lsp

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// diagnosticsDebounce is how long to wait for a quiet period after the last
// publish before treating a document's diagnostics as settled.
const diagnosticsDebounce = 300 * time.Millisecond

// serverStarter starts (and initializes) a language server. It is injectable so
// tests can substitute a stub over an in-memory pipe instead of spawning a real
// server.
type serverStarter func(ctx context.Context, command []string, root string) (lspServer, error)

func defaultStarter(ctx context.Context, command []string, root string) (lspServer, error) {
	return StartServer(ctx, command, root)
}

// Manager is the single entry point the rest of ZERO uses for LSP. It owns one
// long-lived server per language (lazily started, reused across calls — starting
// gopls per edit would be far too slow), routes a file to the right server, and
// degrades to "no diagnostics" when no server is available. Safe for concurrent
// use.
type Manager struct {
	workspaceRoot string
	debounce      time.Duration
	starter       serverStarter

	mu       sync.Mutex
	sessions map[string]*session // keyed by server binary
}

// NewManager creates a Manager rooted at workspaceRoot.
func NewManager(workspaceRoot string) *Manager {
	return newManagerWithStarter(workspaceRoot, defaultStarter)
}

func newManagerWithStarter(workspaceRoot string, starter serverStarter) *Manager {
	return &Manager{
		workspaceRoot: workspaceRoot,
		debounce:      diagnosticsDebounce,
		starter:       starter,
		sessions:      map[string]*session{},
	}
}

// Check syncs the file's text to the right language server and returns the
// settled diagnostics. A file whose extension has no configured/available server
// returns (nil, nil): missing diagnostics are a graceful degrade, not an error.
func (m *Manager) Check(ctx context.Context, path, text string) ([]Diagnostic, error) {
	command, ok := ServerFor(path)
	if !ok {
		return nil, nil
	}
	languageID, _ := LanguageID(path)
	abs := m.absPath(path)
	uri := PathToURI(abs)

	sess, err := m.sessionFor(ctx, command)
	if err != nil {
		// A configured extension whose language-server binary isn't installed must
		// degrade exactly like an unsupported extension — LSP is an opportunistic
		// layer, not a hard dependency. Only a missing binary degrades; any other
		// start failure still surfaces.
		if isServerUnavailable(err) {
			return nil, nil
		}
		return nil, err
	}
	baseline := sess.publishBaseline(uri)
	if err := sess.sync(ctx, abs, languageID, text); err != nil {
		return nil, err
	}
	// If no publish newer than baseline arrived (server too slow / ctx expired),
	// return no diagnostics rather than a stale prior result for the new text —
	// a missing signal degrades like a missing server, not a false "compiles".
	if !sess.waitForDiagnostics(ctx, uri, m.debounce, baseline) {
		return nil, nil
	}
	return sess.diagnosticsFor(uri), nil
}

// DiagnosticsFor returns the most recently published diagnostics for a file
// without re-syncing. Empty when no server is running for it.
func (m *Manager) DiagnosticsFor(path string) []Diagnostic {
	command, ok := ServerFor(path)
	if !ok {
		return nil
	}
	m.mu.Lock()
	sess := m.sessions[command[0]]
	m.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.diagnosticsFor(PathToURI(m.absPath(path)))
}

// HasErrors reports whether the file currently has any error-severity diagnostic.
func (m *Manager) HasErrors(path string) bool {
	return hasErrors(m.DiagnosticsFor(path))
}

// Shutdown stops every running server, returning the joined errors of any that
// failed to exit cleanly (a leaked language-server process is worth surfacing).
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*session{}
	m.mu.Unlock()
	// Shut servers down concurrently: each Shutdown can block up to shutdownGrace,
	// so a serial loop over N servers would take N×grace. One goroutine each, joined,
	// bounds the whole shutdown to a single grace window (L19).
	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)
	for _, sess := range sessions {
		wg.Add(1)
		go func(sess *session) {
			defer wg.Done()
			if err := sess.server.Shutdown(ctx); err != nil {
				errsMu.Lock()
				errs = append(errs, err)
				errsMu.Unlock()
			}
		}(sess)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// sessionFor returns the running session for a server command, starting one if
// needed. Concurrent first-callers double-check under the lock so only one server
// is kept; a loser shuts its extra server down.
func (m *Manager) sessionFor(ctx context.Context, command []string) (*session, error) {
	key := command[0]
	m.mu.Lock()
	if sess, ok := m.sessions[key]; ok {
		if !sess.client.IsClosed() {
			m.mu.Unlock()
			return sess, nil
		}
		// The cached session's client has died (server crashed/exited/malformed
		// frame). Evict it so a fresh server is started below — otherwise every
		// later diagnostic fails forever against a permanently-dead session (H4).
		delete(m.sessions, key)
		m.mu.Unlock()
		_ = sess.server.Shutdown(context.Background()) // best-effort reap of the dead server
	} else {
		m.mu.Unlock()
	}

	server, err := m.starter(ctx, command, m.workspaceRoot)
	if err != nil {
		return nil, err
	}
	sess := newSession(server)

	m.mu.Lock()
	if existing, ok := m.sessions[key]; ok && !existing.client.IsClosed() {
		m.mu.Unlock()
		_ = server.Shutdown(context.Background()) // lost the start race; discard ours
		return existing, nil
	}
	m.sessions[key] = sess // install ours (replacing an absent or already-dead entry)
	m.mu.Unlock()
	return sess, nil
}

// isServerUnavailable reports whether a start error is just a missing binary
// (exec could not find the language server on PATH), which should degrade to
// "no diagnostics" rather than fail the run.
func isServerUnavailable(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}

func (m *Manager) absPath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(m.workspaceRoot, path)
}
