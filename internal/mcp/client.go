package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/execution"
	"github.com/Gitlawb/zero/internal/imageinput"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type RemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// MimeType names what a non-text block holds. Additive and omitempty, so
	// servers that never send it decode exactly as before. Image blocks with
	// valid data are forwarded on Result.Images; MimeType is what lets a
	// remaining dropped block (audio, resource, failed decode) be described
	// instead of vanishing (#823).
	MimeType string `json:"mimeType,omitempty"`
	// Data is the MCP image block's base64 payload. Additive and omitempty so a
	// result that never sent it still unmarshals. Decoded only for type
	// "image"; other types leave it unread.
	Data string `json:"data,omitempty"`
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
	cleanup func()

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

	client := &Client{
		server:  server,
		cmd:     cmd,
		stdin:   stdin,
		reader:  newMessageReader(stdout),
		writer:  newMessageWriter(stdin),
		nextID:  1,
		cleanup: cleanup,
	}
	cleanupTransferred = true
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

// DroppedContentSummary describes the blocks that were not forwarded, e.g.
// "1 audio/wav block" or "2 resource blocks, 1 image/png block". It returns ""
// when every block is text or an image that ImageBlocks successfully
// forwarded, so a caller adds nothing to the ordinary case.
//
// Image payloads ride Result.Images. Audio, embedded resources, structured
// content, and image blocks whose data cannot be decoded still have nowhere
// to go. Images skipped because they would exceed the aggregate byte budget
// are also named so the model hears that a valid screenshot was dropped.
//
// Only images actually kept by ImageBlocks are omitted from the note. An
// image that would decode in isolation but was skipped by the aggregate cap
// is still named, otherwise the model would not hear that a valid screenshot
// was dropped.
//
// Counts are grouped by mime type and ordered by first appearance, so the same
// result always produces the same sentence.
func DroppedContentSummary(content []Content) string {
	_, disp := forwardImages(content)
	return droppedContentNote(content, disp, dispDropped, dispBudgetExceeded, dispUninspected)
}

// ImageBlocks converts MCP image content into the same ImageBlock channel
// capture tools already use. Blocks that cannot be decoded, exceed
// imageinput.MaxImageBytes individually, sniff to a type outside the provider
// allow-list, or would push the result over an aggregate
// imageinput.MaxImageBytes budget, are left for DroppedContentSummary to name.
//
// The aggregate cap is the same 10 MiB as the per-image cap: a server that
// returns many individually valid images must not retain all of them in
// Result.Images. Once the next valid image would exceed the remaining
// budget it is skipped; a later smaller image may still fit. Later image
// payloads are not decoded only once remaining is zero; a leftover residue
// still fully decodes the next candidate before the length check rejects it.
func ImageBlocks(content []Content) []zeroruntime.ImageBlock {
	images, _ := forwardImages(content)
	return images
}

// itemDisp is the per-item forwarding/drop disposition produced by the
// single-pass conversion. DroppedContentSummary is built from this so a
// valid image is never base64-decoded a second time just to name what was
// kept versus dropped.
type itemDisp uint8

const (
	dispText itemDisp = iota
	dispForwarded
	dispDropped
	dispBudgetExceeded
	dispUninspected
)

// decodeImageBase64 is the MCP image payload decoder. Tests replace it to
// count decode attempts; production uses standard base64.
var decodeImageBase64 = base64.StdEncoding.DecodeString

func forwardImages(content []Content) ([]zeroruntime.ImageBlock, []itemDisp) {
	disp := make([]itemDisp, len(content))
	var images []zeroruntime.ImageBlock
	remaining := imageinput.MaxImageBytes
	for i, item := range content {
		if item.Type == "text" {
			disp[i] = dispText
			continue
		}
		if item.Type == "image" {
			if remaining == 0 {
				disp[i] = dispUninspected
				continue
			}
			if image, ok := imageBlockFromContent(item); ok {
				if len(image.Data) <= remaining {
					images = append(images, image)
					remaining -= len(image.Data)
					disp[i] = dispForwarded
					continue
				}
				disp[i] = dispBudgetExceeded
				continue
			}
		}
		disp[i] = dispDropped
	}
	return images, disp
}

func droppedContentNote(content []Content, disp []itemDisp, kinds ...itemDisp) string {
	labels := make([]string, 0, len(content))
	counts := make(map[string]int, len(content))
	for i, item := range content {
		if i >= len(disp) || !dispKind(disp[i], kinds) {
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

func dispKind(got itemDisp, kinds []itemDisp) bool {
	for _, kind := range kinds {
		if got == kind {
			return true
		}
	}
	return false
}

func imageBlockFromContent(item Content) (zeroruntime.ImageBlock, bool) {
	if item.Type != "image" {
		return zeroruntime.ImageBlock{}, false
	}
	raw := strings.TrimSpace(item.Data)
	if raw == "" {
		return zeroruntime.ImageBlock{}, false
	}
	// EncodedLen(MaxImageBytes) is the encoded size of an image that decodes
	// to exactly the inclusive cap, including "==" padding. DecodedLen is an
	// upper bound and reports cap+2 for that input, so using it here would
	// reject a valid at-limit PNG. The post-decode len(data) check is the
	// exact backstop.
	if len(raw) > base64.StdEncoding.EncodedLen(imageinput.MaxImageBytes) {
		return zeroruntime.ImageBlock{}, false
	}
	data, err := decodeImageBase64(raw)
	if err != nil {
		return zeroruntime.ImageBlock{}, false
	}
	if len(data) == 0 || len(data) > imageinput.MaxImageBytes {
		return zeroruntime.ImageBlock{}, false
	}
	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	mediaType := zeroruntime.NormalizeImageMediaType(http.DetectContentType(data[:sniffLen]))
	if mediaType == "" {
		return zeroruntime.ImageBlock{}, false
	}
	return zeroruntime.ImageBlock{MediaType: mediaType, Data: data}, true
}
