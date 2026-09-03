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

	"github.com/Gitlawb/zero/internal/execution"
)

type RemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// MimeType names what a non-text block holds. Decoded but not yet forwarded:
	// it is what lets a dropped block be described to the model instead of
	// vanishing (#823). Servers that omit it still decode fine.
	MimeType string `json:"mimeType,omitempty"`
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

// startupDisclosureError carries a launch disclosure out through a failure.
//
// A launched process is a fact about the past: once Start has succeeded the
// disclosure is true whatever the handshake does next. The client is the only
// thing that holds it, and the failure paths close and discard the client, so
// without this the fact dies with the connection it was attached to.
type startupDisclosureError struct {
	err     error
	notices []string
}

func (e *startupDisclosureError) Error() string { return e.err.Error() }
func (e *startupDisclosureError) Unwrap() error { return e.err }

// startupNoticesFromError recovers a launch disclosure from a failed connect.
func startupNoticesFromError(err error) []string {
	var disclosure *startupDisclosureError
	if errors.As(err, &disclosure) {
		return disclosure.notices
	}
	return nil
}

// startupDisclosing is the optional interface a client implements when its
// LAUNCH carried a least-privilege disclosure. A network server launches no
// local process, so it does not implement this and reports nothing, which is the
// correct answer rather than an empty one.
type startupDisclosing interface {
	StartupNotices() []string
}

// StartupNotices reports the disclosures that applied to this server's launch.
func (client *Client) StartupNotices() []string {
	if client == nil || len(client.startupNotices) == 0 {
		return nil
	}
	return append([]string(nil), client.startupNotices...)
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
	cleanup func()
	// startupNotices are the least-privilege disclosures that applied to THIS
	// server's launch.
	//
	// THE FACT DESCRIBES STARTUP, SO IT CANNOT BE RECOVERED FROM A TOOL RESULT.
	// A stdio server prepared under the weakened token runs for the whole session,
	// and connectStdio used to keep only the command and its cleanup, so nothing
	// downstream could tell the operator that the process serving these tools had
	// reduced write confinement. Kept typed here and rendered exactly once at
	// registration rather than pasted onto every later tool result.
	startupNotices []string

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
	return ConnectWithOptions(ctx, server, ConnectOptions{})
}

type ConnectOptions struct {
	Execution     *execution.Runner
	WorkspaceRoot string
}

func ConnectWithOptions(ctx context.Context, server Server, options ConnectOptions) (ToolClient, error) {
	switch server.Type {
	case ServerTypeStdio:
		return connectStdio(ctx, server, options)
	case ServerTypeHTTP:
		return connectNetwork(ctx, server)
	case ServerTypeSSE:
		return connectRemoteSSE(ctx, server)
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q for server %s", server.Type, server.Name)
	}
}

// maxStderrCapture bounds how much of an MCP server's stderr is retained. The
// buffer is only read when initialize fails (early in the process life), so a
// modest head is plenty; this stops a long-lived, chatty server from growing the
// buffer without bound for the whole process lifetime.
const maxStderrCapture = 64 * 1024

// boundedBuffer is a concurrency-safe io.Writer that retains at most cap bytes
// (the earliest ones) and silently discards the rest, so attaching it as
// cmd.Stderr can never leak unbounded memory. os/exec writes to it from its own
// copy goroutine, hence the mutex.
type boundedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	cap int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := b.cap - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
		} else {
			b.buf.Write(p)
		}
	}
	// Report the full length so the writer never sees a short write.
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func connectStdio(ctx context.Context, server Server, options ConnectOptions) (*Client, error) {
	var cmd *exec.Cmd
	var cleanup func()
	var plannedEnforcement execution.Enforcement
	// Retained from the prepared plan rather than dropped: for a wrapped plan the
	// adapter, not cmd.Start, owns whether the requested server process exists.
	var adapterReport func() (execution.AdapterReport, error)
	var ownedLaunch bool
	cleanupTransferred := false
	defer func() {
		if cleanup != nil && !cleanupTransferred {
			cleanup()
		}
	}()
	if options.Execution != nil {
		workspaceRoot := strings.TrimSpace(options.WorkspaceRoot)
		if workspaceRoot == "" {
			return nil, fmt.Errorf("start MCP server %s: execution workspace root is required", server.Name)
		}
		prepared, err := options.Execution.Prepare(ctx, execution.Request{
			Origin:           execution.OriginMCPServer,
			Mode:             execution.ModeDurable,
			Command:          execution.Command{Name: server.Command, Args: append([]string(nil), server.Args...), Env: mergeProcessEnv(server.Env)},
			WorkingDirectory: workspaceRoot,
			WorkspaceRoots:   []string{workspaceRoot},
			Approval:         execution.ApprovalContext{PolicyVersion: execution.PolicyVersion},
		})
		if err != nil {
			return nil, fmt.Errorf("start MCP server %s: %w", server.Name, err)
		}
		cmd = prepared.Command
		cleanup = prepared.Cleanup
		plannedEnforcement = prepared.Enforcement
		adapterReport = prepared.Report
		ownedLaunch = prepared.ChildLaunchOwnedByAdapter
	} else {
		cmd = exec.CommandContext(ctx, server.Command, server.Args...)
		cmd.Env = mergeProcessEnv(server.Env)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdin for %s: %w", server.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdout for %s: %w", server.Name, err)
	}
	stderr := &boundedBuffer{cap: maxStderrCapture}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server %s: %w", server.Name, err)
	}

	// PUBLISHED AT START, not at return. Registration abandons a server that
	// exceeds the connect timeout, and everything below this line (initialize, and
	// tools/list above it in the caller) can hang past that. Announcing the launch
	// here is what lets an abandoned attempt still disclose the confinement its
	// process ran under.
	//
	// EXCEPT WHEN START IS NOT THE LAUNCH. For a wrapped plan the process started
	// above is the sandbox helper, which creates the MCP server only after marker,
	// ACL, network, SID and token setup, any of which can fail leaving no server at
	// all. Publishing here would make the durable delivery machinery reliably
	// announce a confinement nothing ever ran under. Those plans publish from
	// publishAdapterLaunch below, once the adapter has stated the fact.
	if !ownedLaunch {
		publishLaunch(ctx, plannedEnforcement.Notices)
	}
	// publishAdapterLaunch announces a wrapped plan's launch, but only if the
	// adapter confirms the requested child was created. Called on both ways this
	// attempt can end, which is also where an attempt abandoned at the connect
	// timeout eventually arrives, so a late disclosure is still delivered once.
	publishAdapterLaunch := func() {
		if !ownedLaunch || adapterReport == nil {
			return
		}
		report, err := adapterReport()
		if err != nil || report.ChildLaunched == nil || !*report.ChildLaunched {
			return
		}
		publishLaunch(ctx, plannedEnforcement.Notices)
	}

	client := &Client{
		server:  server,
		cmd:     cmd,
		stdin:   stdin,
		reader:  newMessageReader(stdout),
		writer:  newMessageWriter(stdin),
		nextID:  1,
		cleanup: cleanup,
		// Recorded only now, AFTER Start returned. Everything above returns early,
		// so a prepare failure or an executable that could not be launched records
		// nothing: same launch-state rule hooks and plugins use, expressed by where
		// this assignment sits rather than by another outcome-kind switch.
		startupNotices: append([]string(nil), plannedEnforcement.Notices...),
	}
	cleanupTransferred = true
	if err := client.initialize(ctx); err != nil {
		// THE LAUNCH ALREADY HAPPENED, so the fact has to leave through the error.
		// Start succeeded above, which means the process ran under the planned token
		// and may have done filesystem work before the handshake failed. Returning a
		// bare error discards the client, and with it the only carrier the notices
		// had, so the operator was told the server was unavailable and not that it
		// had already run without the write jail.
		_ = client.Close()
		publishAdapterLaunch()
		message := strings.TrimSpace(stderr.String())
		failure := fmt.Errorf("initialize MCP server %s: %w", server.Name, err)
		if message != "" {
			failure = fmt.Errorf("initialize MCP server %s: %w: %s", server.Name, err, message)
		}
		return nil, &startupDisclosureError{err: failure, notices: client.StartupNotices()}
	}
	publishAdapterLaunch()
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
	if client.cleanup != nil {
		client.cleanup()
		client.cleanup = nil
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

// DroppedContentSummary describes the blocks TextContent discards, e.g.
// "1 image/png block" or "2 resource blocks, 1 audio/wav block". It returns ""
// when a result is entirely text, so a caller adds nothing to the ordinary case.
//
// This exists because dropping silently is the worst available behaviour. A
// screenshot server returns a valid image, TextContent keeps nothing, and the
// call is reported as "(empty MCP tool result)" — so the model concludes the
// tool produced nothing and usually retries, burning another call on the same
// empty answer. Naming what came back costs nothing and ends that loop even
// though the payload still cannot be forwarded.
//
// Counts are grouped by mime type and ordered by first appearance, so the same
// result always produces the same sentence.
func DroppedContentSummary(content []Content) string {
	labels := make([]string, 0, len(content))
	counts := make(map[string]int, len(content))
	for _, item := range content {
		if item.Type == "text" {
			continue
		}
		// Prefer the mime type: "image/png" tells the reader more than "image".
		// A server may omit it, so fall back to the block type rather than
		// printing an empty label.
		label := strings.TrimSpace(item.MimeType)
		if label == "" {
			label = strings.TrimSpace(item.Type)
		}
		if label == "" {
			label = "unknown"
		}
		if _, seen := counts[label]; !seen {
			labels = append(labels, label)
		}
		counts[label]++
	}
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		part := fmt.Sprintf("%d %s block", counts[label], label)
		if counts[label] != 1 {
			part += "s"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}
