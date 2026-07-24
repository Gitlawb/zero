// Package acp implements the Agent Client Protocol (ACP) surface for ZERO: a
// JSON-RPC 2.0 peer spoken over stdio (newline-delimited JSON) so editors such
// as Zed, JetBrains, and Neovim can drive ZERO's agent core as a backend.
//
// The protocol shapes (method names, message fields) are derived solely from the
// public, Apache-licensed ACP specification at agentclientprotocol.com and the
// agentclientprotocol/agent-client-protocol repository (schema/v1). All logic
// here is written originally against ZERO's own interfaces.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// JSON-RPC 2.0 standard error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "<nil rpc error>"
	}
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// RPCError builds a protocol error to return from a handler. Use it when a
// handler needs to control the wire-level error code; any other error a handler
// returns is reported as a generic internal error.
func RPCError(code int, message string) error {
	return &rpcError{Code: code, Message: message}
}

// rpcMessage is the union of request, response, and notification frames. The
// shape is disambiguated by which fields are present (per JSON-RPC 2.0):
//   - request:      method + id
//   - notification: method, no id
//   - response:     id + (result | error), no method
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (m rpcMessage) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }
func (m rpcMessage) isRequest() bool  { return m.Method != "" && len(m.ID) > 0 }
func (m rpcMessage) isNotify() bool   { return m.Method != "" && len(m.ID) == 0 }

// HandlerFunc handles an inbound request and returns the result to encode, or an
// error. Returning an *rpcError controls the wire code; any other error becomes
// a generic internal error.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// NotifyFunc handles an inbound notification (no response is sent).
type NotifyFunc func(ctx context.Context, params json.RawMessage)

// Conn is a JSON-RPC 2.0 peer over a single ndjson stream pair. It both serves
// inbound requests/notifications (via registered handlers) and issues outbound
// requests/notifications — needed because ACP is bidirectional (the agent calls
// the client for session/request_permission, fs/*, terminal/*).
type Conn struct {
	reader       *bufio.Reader
	readerCloser io.Closer
	w            io.Writer

	writeMu sync.Mutex // serializes all writes to w

	handlers  map[string]HandlerFunc
	notifiers map[string]NotifyFunc

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
	closed  bool

	wg sync.WaitGroup // tracks in-flight inbound handlers
}

// NewConn builds a peer reading ndjson from r and writing ndjson to w. If r is
// closable, Serve closes it to interrupt an idle read when its context is
// cancelled.
func NewConn(r io.Reader, w io.Writer) *Conn {
	readerCloser, _ := r.(io.Closer)
	return &Conn{
		reader:       bufio.NewReader(r),
		readerCloser: readerCloser,
		w:            w,
		handlers:     make(map[string]HandlerFunc),
		notifiers:    make(map[string]NotifyFunc),
		pending:      make(map[int64]chan rpcMessage),
	}
}

// Handle registers a request handler for method.
func (c *Conn) Handle(method string, fn HandlerFunc) { c.handlers[method] = fn }

// HandleNotify registers a notification handler for method.
func (c *Conn) HandleNotify(method string, fn NotifyFunc) { c.notifiers[method] = fn }

// Serve runs the read loop until the stream ends, ctx is cancelled, or a fatal
// decode error occurs. Inbound requests and notifications are dispatched on
// their own goroutines so a long-running handler (e.g. session/prompt) never
// blocks the loop from delivering session/cancel or a permission response.
func (c *Conn) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	readLoopDone := make(chan struct{})
	var endReadOnce sync.Once
	readInterrupted := false
	endRead := func(interrupt bool) {
		endReadOnce.Do(func() {
			readInterrupted = interrupt
			if interrupt && c.readerCloser != nil {
				_ = c.readerCloser.Close()
			}
			close(readLoopDone)
		})
	}
	var readerWatcher sync.WaitGroup
	readerWatcher.Add(1)
	go func() {
		defer readerWatcher.Done()
		select {
		case <-ctx.Done():
			endRead(true)
		case <-readLoopDone:
		}
	}()
	// On exit, cancel in-flight handlers (so a blocked outbound Call unblocks via
	// ctx) and then wait for them to finish writing their responses. Without this,
	// a finite input stream (e.g. piped ndjson that EOFs right after a request)
	// would race the dispatch goroutine and drop the response.
	defer func() {
		endRead(false)
		cancel()
		readerWatcher.Wait()
		c.wg.Wait()
	}()

	for {
		// One ndjson value per line. Framing per line (rather than streaming the
		// decoder) keeps a single malformed line from making the whole connection
		// unrecoverable — we report -32700 and continue.
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			// Claim terminal completion before handling a partial final frame so
			// concurrent cancellation cannot close the reader or mask this error.
			endRead(false)
		}
		if len(bytes.TrimSpace(line)) > 0 {
			c.handleLine(ctx, line)
		}
		if err != nil {
			c.failAllPending(err)
			if errors.Is(err, io.EOF) || readInterrupted {
				return nil
			}
			return err
		}
	}
}

// handleLine decodes and dispatches one ndjson frame. A parse failure replies
// with -32700 (id null) and keeps the connection alive.
func (c *Conn) handleLine(ctx context.Context, line []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(bytes.TrimSpace(line), &msg); err != nil {
		c.writeError(json.RawMessage("null"), &rpcError{Code: codeParseError, Message: "parse error"})
		return
	}
	if msg.JSONRPC != "" && msg.JSONRPC != "2.0" {
		if len(msg.ID) > 0 {
			c.writeError(msg.ID, &rpcError{Code: codeInvalidRequest, Message: "unsupported jsonrpc version"})
		}
		return
	}
	switch {
	case msg.isResponse():
		c.deliver(msg)
	case msg.isRequest():
		c.wg.Add(1)
		go func(m rpcMessage) {
			defer c.wg.Done()
			c.dispatchRequest(ctx, m)
		}(msg)
	case msg.isNotify():
		if fn := c.notifiers[msg.Method]; fn != nil {
			c.wg.Add(1)
			go func(m rpcMessage) {
				defer c.wg.Done()
				fn(ctx, m.Params)
			}(msg)
		}
	default:
		// Malformed frame; reply only if we can identify a request id.
		if len(msg.ID) > 0 {
			c.writeError(msg.ID, &rpcError{Code: codeInvalidRequest, Message: "invalid request"})
		}
	}
}

func (c *Conn) dispatchRequest(ctx context.Context, msg rpcMessage) {
	fn := c.handlers[msg.Method]
	if fn == nil {
		c.writeError(msg.ID, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + msg.Method})
		return
	}
	result, err := fn(ctx, msg.Params)
	if err != nil {
		var re *rpcError
		if errors.As(err, &re) {
			c.writeError(msg.ID, re)
		} else {
			c.writeError(msg.ID, &rpcError{Code: codeInternalError, Message: err.Error()})
		}
		return
	}
	c.writeResult(msg.ID, result)
}

// Call issues an outbound request and blocks until the response arrives, ctx is
// cancelled, or the connection closes.
func (c *Conn) Call(ctx context.Context, method string, params any, result any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("acp: connection closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	idRaw, _ := json.Marshal(id)
	if err := c.write(rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: raw}); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// Notify issues an outbound notification (no response expected).
func (c *Conn) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: raw})
}

func (c *Conn) deliver(msg rpcMessage) {
	var id int64
	if err := json.Unmarshal(msg.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	// Non-blocking: the pending channel is cap-1 and may have been abandoned (e.g.
	// a Call cancelled by session/cancel). A duplicate or late response frame must
	// never block the read-loop goroutine.
	select {
	case ch <- msg:
	default:
	}
}

func (c *Conn) failAllPending(err error) {
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = make(map[int64]chan rpcMessage)
	c.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- rpcMessage{Error: &rpcError{Code: codeInternalError, Message: err.Error()}}:
		default:
		}
	}
}

func (c *Conn) writeResult(id json.RawMessage, result any) {
	raw, err := json.Marshal(result)
	if err != nil {
		c.writeError(id, &rpcError{Code: codeInternalError, Message: err.Error()})
		return
	}
	_ = c.write(rpcMessage{JSONRPC: "2.0", ID: id, Result: raw})
}

func (c *Conn) writeError(id json.RawMessage, e *rpcError) {
	_ = c.write(rpcMessage{JSONRPC: "2.0", ID: id, Error: e})
}

func (c *Conn) write(msg rpcMessage) error {
	msg.JSONRPC = "2.0"
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// marshalParams encodes params, treating nil as an absent params field.
func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(params)
}
