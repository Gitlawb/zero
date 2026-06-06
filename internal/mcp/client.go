package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type ToolClient interface {
	ListTools(context.Context) ([]RemoteTool, error)
	CallTool(context.Context, string, map[string]any) (CallToolResult, error)
	Close() error
}

type Client struct {
	server  Server
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *messageReader
	writer  *messageWriter
	mu      sync.Mutex
	closeMu sync.Mutex
	nextID  int

	// dispatchMu guards the response-dispatch state shared with the single
	// reader goroutine. It is never held across a blocking read.
	dispatchMu sync.Mutex
	readerOnce sync.Once
	pending    map[int]chan dispatchResult
	readErr    error
	readDone   bool
}

// dispatchResult carries one matched JSON-RPC response (or a terminal reader
// error) to a waiting caller.
type dispatchResult struct {
	message rpcMessage
	err     error
}

const stdioCloseWaitTimeout = 500 * time.Millisecond

const (
	// initializeTimeout bounds the MCP handshake so a non-responsive peer fails
	// fast instead of hanging startup.
	initializeTimeout = 30 * time.Second
)

func Connect(ctx context.Context, server Server) (ToolClient, error) {
	switch server.Type {
	case ServerTypeStdio:
		return connectStdio(ctx, server)
	case ServerTypeHTTP:
		return connectNetwork(ctx, server)
	case ServerTypeSSE:
		return connectRemoteSSE(ctx, server)
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q for server %s", server.Type, server.Name)
	}
}

func connectStdio(ctx context.Context, server Server) (*Client, error) {
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Env = mergeProcessEnv(server.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdin for %s: %w", server.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdout for %s: %w", server.Name, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server %s: %w", server.Name, err)
	}

	client := &Client{
		server: server,
		cmd:    cmd,
		stdin:  stdin,
		reader: newMessageReader(stdout),
		writer: newMessageWriter(stdin),
		nextID: 1,
	}
	if err := client.initialize(ctx); err != nil {
		_ = client.Close()
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("initialize MCP server %s: %w: %s", server.Name, err, message)
		}
		return nil, fmt.Errorf("initialize MCP server %s: %w", server.Name, err)
	}
	return client, nil
}

// initialize performs the MCP handshake under a bounded timeout so a
// non-responsive peer fails fast instead of hanging startup.
func (client *Client) initialize(ctx context.Context) error {
	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := client.request(initCtx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "zero",
			"version": "dev",
		},
	}, &result); err != nil {
		return err
	}
	return client.notify("notifications/initialized", map[string]any{})
}

func (client *Client) ListTools(ctx context.Context) ([]RemoteTool, error) {
	var result struct {
		Tools []RemoteTool `json:"tools"`
	}
	if err := client.request(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (client *Client) CallTool(ctx context.Context, name string, args map[string]any) (CallToolResult, error) {
	var result CallToolResult
	if err := client.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result); err != nil {
		return CallToolResult{}, err
	}
	return result, nil
}

func (client *Client) Close() error {
	client.closeMu.Lock()
	defer client.closeMu.Unlock()

	// Fail any callers still waiting on a response. The blocking read in the
	// reader goroutine is released below when stdin closes and the process
	// exits (or is killed), EOFing stdout.
	client.failAll(errors.New("MCP client closed"))

	var err error
	stdin := client.stdin
	cmd := client.cmd
	client.stdin = nil
	client.cmd = nil

	if stdin != nil {
		err = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		waitDone := make(chan error, 1)
		go func() {
			waitDone <- cmd.Wait()
		}()

		select {
		case waitErr := <-waitDone:
			if err == nil && waitErr != nil {
				err = waitErr
			}
		case <-time.After(stdioCloseWaitTimeout):
			killed := false
			killErr := cmd.Process.Kill()
			if killErr == nil {
				killed = true
			} else if err == nil && !errors.Is(killErr, os.ErrProcessDone) {
				err = killErr
			}
			waitErr := <-waitDone
			if err == nil && waitErr != nil && !killed {
				err = waitErr
			}
		}
	}
	return err
}

func (client *Client) request(ctx context.Context, method string, params any, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	client.ensureReader()

	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}

	// Allocate an id, register a response channel, and write the request while
	// holding client.mu. The mutex serializes writes and id allocation but is
	// released before the (potentially unbounded) wait for the response, so a
	// hung server never holds the lock and blocks other callers/Close.
	client.mu.Lock()
	id := client.nextID
	client.nextID++

	responses := make(chan dispatchResult, 1)
	client.dispatchMu.Lock()
	if client.readDone {
		readErr := client.readErr
		client.dispatchMu.Unlock()
		client.mu.Unlock()
		if readErr != nil {
			return readErr
		}
		return fmt.Errorf("MCP %s failed: connection closed", method)
	}
	client.pending[id] = responses
	client.dispatchMu.Unlock()

	if err := client.writer.write(rpcMessage{
		ID:     id,
		Method: method,
		Params: rawParams,
	}); err != nil {
		client.removePending(id)
		client.mu.Unlock()
		return err
	}
	client.mu.Unlock()

	select {
	case <-ctx.Done():
		client.removePending(id)
		return ctx.Err()
	case result := <-responses:
		if result.err != nil {
			return result.err
		}
		message := result.message
		if message.Error != nil {
			return fmt.Errorf("MCP %s failed: %s", method, message.Error.Message)
		}
		if target != nil && len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, target); err != nil {
				return fmt.Errorf("decode MCP %s result: %w", method, err)
			}
		}
		return nil
	}
}

// ensureReader lazily starts the single reader goroutine. It runs once per
// client; subsequent calls are no-ops.
func (client *Client) ensureReader() {
	client.readerOnce.Do(func() {
		client.dispatchMu.Lock()
		if client.pending == nil {
			client.pending = make(map[int]chan dispatchResult)
		}
		client.dispatchMu.Unlock()
		go client.readLoop()
	})
}

// readLoop is the single consumer of the message reader. It dispatches each
// response to the waiting caller by id and, on a terminal read error (EOF,
// closed pipe, or a Close-triggered cancel), fails all pending callers so none
// block forever.
func (client *Client) readLoop() {
	for {
		message, err := client.reader.read()
		if err != nil {
			client.failAll(err)
			return
		}
		if message.ID == nil {
			continue
		}
		id, ok := rpcMessageID(message.ID)
		if !ok {
			continue
		}
		client.dispatchMu.Lock()
		responses := client.pending[id]
		if responses != nil {
			delete(client.pending, id)
		}
		client.dispatchMu.Unlock()
		if responses != nil {
			responses <- dispatchResult{message: message}
		}
	}
}

func (client *Client) removePending(id int) {
	client.dispatchMu.Lock()
	delete(client.pending, id)
	client.dispatchMu.Unlock()
}

func (client *Client) failAll(err error) {
	client.dispatchMu.Lock()
	if client.readDone {
		client.dispatchMu.Unlock()
		return
	}
	client.readDone = true
	client.readErr = err
	pending := client.pending
	client.pending = make(map[int]chan dispatchResult)
	client.dispatchMu.Unlock()
	for _, responses := range pending {
		responses <- dispatchResult{err: err}
	}
}

// rpcMessageID extracts the integer id from a JSON-RPC id value across the
// numeric/string encodings a server may use.
func rpcMessageID(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		parsed, err := strconv.Atoi(typed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func rpcIDMatches(value any, id int) bool {
	switch typed := value.(type) {
	case int:
		return typed == id
	case int64:
		return typed == int64(id)
	case float64:
		return typed == float64(id)
	case json.Number:
		parsed, err := typed.Int64()
		return err == nil && parsed == int64(id)
	case string:
		return typed == strconv.Itoa(id)
	default:
		return false
	}
}

func (client *Client) notify(method string, params any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return client.writer.write(rpcMessage{
		Method: method,
		Params: rawParams,
	})
}

func mergeProcessEnv(env map[string]string) []string {
	merged := append([]string{}, os.Environ()...)
	for key, value := range env {
		merged = append(merged, key+"="+value)
	}
	return merged
}

func TextContent(content []Content) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
