package tui

import (
	"context"
	"testing"
)

// The LSP manager is built once at construction (when cwd is known) and reused —
// a fresh manager per run would cold-start gopls on the first edit of every turn.
func TestNewModelBuildsSharedLSPManager(t *testing.T) {
	m := newModel(context.Background(), Options{Cwd: t.TempDir()})
	if m.lspManager == nil {
		t.Fatal("expected a session-long lspManager when cwd is set")
	}
	// The pointer is stable across model copies (Bubble Tea passes the model by
	// value every Update), so every run shares the same warm servers.
	copied := m
	if copied.lspManager != m.lspManager {
		t.Fatal("lspManager pointer must survive a model copy")
	}
}

// shutdownLSPManager must be nil-safe: a model built without a cwd has no manager,
// and quitting it must not panic.
func TestShutdownLSPManagerNilSafe(t *testing.T) {
	m := model{}
	if m.lspManager != nil {
		t.Fatal("expected no manager on a zero-value model")
	}
	m.shutdownLSPManager() // must not panic
}
