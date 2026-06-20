package lsp

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// shutdownGrace bounds how long Shutdown waits for the server to exit cleanly
// before killing the process.
const shutdownGrace = 2 * time.Second

// Server is a spawned language-server process plus the JSON-RPC client speaking
// to it over stdio, with the LSP initialize handshake already completed.
type Server struct {
	cmd    *exec.Cmd
	client *Client
	stdin  io.WriteCloser
}

// StartServer spawns the given command, wires a Client to its stdio, and performs
// the LSP initialize/initialized handshake rooted at rootPath. On any failure the
// process is torn down before returning.
func StartServer(ctx context.Context, command []string, rootPath string) (*Server, error) {
	if len(command) == 0 {
		return nil, errors.New("empty language-server command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// The server's stderr is diagnostic noise for our purposes; discard it so it
	// can't block on a full pipe.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	server := &Server{cmd: cmd, client: NewClient(stdout, stdin), stdin: stdin}
	if err := performInitialize(ctx, server.client, rootPath); err != nil {
		_ = server.Shutdown(context.Background())
		return nil, err
	}
	return server, nil
}

// Client exposes the JSON-RPC client for document sync / diagnostics (stage 03).
func (s *Server) Client() *Client {
	return s.client
}

// Shutdown sends the LSP shutdown request + exit notification, then waits briefly
// for the process to exit and kills it otherwise. Best-effort: it never returns
// an error for a server that simply ignored the handshake.
func (s *Server) Shutdown(ctx context.Context) error {
	_, _ = s.client.Call(ctx, "shutdown", nil)
	_ = s.client.Notify(ctx, "exit", nil)
	_ = s.client.Close()
	_ = s.stdin.Close()

	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		_ = s.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// Caller's deadline/cancellation fired: kill now instead of waiting out the
		// full grace window. Without this, Shutdown could block up to shutdownGrace
		// per server regardless of the context (L19).
		_ = s.cmd.Process.Kill()
		<-done
	case <-time.After(shutdownGrace):
		_ = s.cmd.Process.Kill()
		<-done
	}
	return nil
}

// performInitialize runs the LSP handshake on an already-connected client. It is
// separate from StartServer so it can be tested against a stub server over an
// in-memory pipe without spawning a real process.
func performInitialize(ctx context.Context, client *Client, rootPath string) error {
	params := InitializeParams{
		ProcessID:    os.Getpid(),
		RootURI:      PathToURI(rootPath),
		Capabilities: clientCapabilities(),
	}
	if _, err := client.Call(ctx, "initialize", params); err != nil {
		return err
	}
	return client.Notify(ctx, "initialized", map[string]any{})
}

// clientCapabilities advertises the minimal capabilities ZERO needs: full-text
// document sync and diagnostics via publishDiagnostics.
func clientCapabilities() map[string]any {
	return map[string]any{
		"textDocument": map[string]any{
			"synchronization": map[string]any{
				"didSave":             true,
				"willSave":            false,
				"dynamicRegistration": false,
			},
			"publishDiagnostics": map[string]any{
				"relatedInformation": true,
			},
		},
	}
}
