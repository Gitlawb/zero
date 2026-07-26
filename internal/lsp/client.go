package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// NotificationHandler receives server->client notifications (e.g.
// textDocument/publishDiagnostics). params is the raw JSON payload. seq is the
// notification's receipt sequence (see Client.NotificationSeq): the count of
// notifications read off the wire, including this one, at the moment it was
// enqueued — NOT when this handler happens to run, which can lag receipt when
// the queue is backed up. A caller that needs to know whether something newer
// than a given point has arrived must compare against seq, not against when
// its own handling code runs.
type NotificationHandler func(method string, params json.RawMessage, seq int64)

// Client speaks JSON-RPC 2.0 with LSP framing (Content-Length headers) over a
// reader/writer pair. It is transport-agnostic: server.go wires it to a process's
// stdout/stdin, and tests wire it to in-memory pipes. Safe for concurrent Call /
// Notify from multiple goroutines.
type Client struct {
	writeMu sync.Mutex // serializes outgoing frames
	writer  io.Writer

	mu      sync.Mutex // guards nextID + pending + handler
	nextID  int64
	pending map[int64]chan rpcResponse
	handler NotificationHandler

	closeOnce sync.Once
	closed    chan struct{}
	readErr   error

	notifyMu     sync.Mutex
	notifyQueue  []notification
	notifyReady  chan struct{}
	notifyClosed bool
	notifySeq    int64 // count of notifications received (enqueued) so far
}

type notification struct {
	method string
	params json.RawMessage
	seq    int64
}

// notifyQueueLimit bounds the notification backlog. A well-behaved handler
// drains far faster than any single burst fills it; sustained overload — a
// language server emitting faster than the single handler can consume, or a
// handler stuck waiting on a re-entrant Call — is a fatal condition for this
// client, not something to paper over by growing the queue without bound.
// Hitting the limit fails the client (see enqueueNotification): IsClosed
// becomes true, and the manager evicts and restarts the session on next use,
// exactly as it does for any other dead client.
const notifyQueueLimit = 4096

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("lsp error %d: %s", e.Code, e.Message)
}

type rpcResponse struct {
	Result json.RawMessage
	Err    *rpcError
}

type outgoingRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type outgoingNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type outgoingReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type incomingMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	Params json.RawMessage `json:"params"`
}

// NewClient starts a client reading framed messages from r and writing to w. It
// spawns a read-loop goroutine that lives until r returns an error (e.g. the
// server process exits); call Close to stop using the client.
func NewClient(r io.Reader, w io.Writer) *Client {
	client := &Client{
		writer:      w,
		pending:     make(map[int64]chan rpcResponse),
		closed:      make(chan struct{}),
		notifyReady: make(chan struct{}, 1),
	}
	go client.notificationLoop()
	go client.readLoop(bufio.NewReader(r))
	return client
}

// SetNotificationHandler installs the handler for server->client notifications.
func (c *Client) SetNotificationHandler(handler NotificationHandler) {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
}

// Call sends a request and blocks until the matching response arrives, the
// context is cancelled, or the connection closes.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// A closed client is unusable: don't register a pending entry that nothing
	// will ever resolve.
	select {
	case <-c.closed:
		return nil, c.readError()
	default:
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(outgoingRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.closed:
		return nil, c.readError()
	case resp := <-ch:
		if resp.Err != nil {
			return nil, resp.Err
		}
		return resp.Result, nil
	}
}

// Notify sends a notification (no response expected).
func (c *Client) Notify(_ context.Context, method string, params any) error {
	return c.write(outgoingNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// Close stops the client and fails any in-flight calls.
func (c *Client) Close() error {
	c.failPending(errors.New("lsp client closed"))
	return nil
}

func (c *Client) readLoop(reader *bufio.Reader) {
	for {
		body, err := readMessage(reader)
		if err != nil {
			c.failPending(err)
			return
		}
		var msg incomingMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue // skip a malformed frame rather than tearing down the session
		}
		hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
		switch {
		case msg.Method != "" && hasID:
			// Server->client request. We don't implement these yet, but a reply is
			// required or the server can block waiting on it (e.g. registerCapability).
			_ = c.write(outgoingReply{JSONRPC: "2.0", ID: msg.ID, Result: nil})
		case msg.Method != "":
			c.enqueueNotification(notification{method: msg.Method, params: msg.Params})
		case hasID:
			var id int64
			if err := json.Unmarshal(msg.ID, &id); err == nil {
				c.deliver(id, rpcResponse{Result: msg.Result, Err: msg.Error})
			}
		}
	}
}

func (c *Client) notificationLoop() {
	for {
		select {
		case <-c.closed:
			return
		case <-c.notifyReady:
			for {
				notification, ok := c.dequeueNotification()
				if !ok {
					break
				}
				c.mu.Lock()
				handler := c.handler
				c.mu.Unlock()
				if handler != nil {
					handler(notification.method, notification.params, notification.seq)
				}
			}
		}
	}
}

// enqueueNotification hands a server notification to the worker loop. It never
// blocks and never silently discards a message the handler could still act on:
// the queue grows instead, up to notifyQueueLimit.
//
// The alternatives to growing are worse. Blocking the read loop when a buffer
// fills is the deadlock this dispatch exists to avoid — a handler that calls
// Client.Call waits for a response frame the blocked reader can no longer
// deliver. Dropping the oldest queued item instead loses protocol state
// permanently: a textDocument/publishDiagnostics for one URI is the server's
// only report for that URI, so discarding it makes session.waitForDiagnostics
// time out and Manager.Check return nothing even though the server published
// findings.
//
// Growth is bounded in practice by how much the server emits while a handler
// runs, and the queue is released as soon as it drains. But "in practice" is
// not a limit: a handler that never returns, or a server that sustains a
// higher rate than the single handler can drain, would otherwise grow this
// queue's full json.RawMessage payloads without bound until the heap gives
// out. notifyQueueLimit turns that into an explicit, observable failure —
// the client is failed and closed — rather than an unbounded retention of
// protocol input this transport has no business trying to buffer forever.
func (c *Client) enqueueNotification(item notification) {
	c.notifyMu.Lock()
	if c.notifyClosed {
		// The worker loop has already stopped, so anything queued now would never
		// be handled. Retaining it would grow the queue for as long as the
		// transport stays readable after Close — Server.Shutdown closes the client
		// before closing stdin, so a server emitting notifications while it handles
		// shutdown/exit keeps the read loop feeding a queue nobody drains.
		c.notifyMu.Unlock()
		return
	}
	if len(c.notifyQueue) >= notifyQueueLimit {
		c.notifyMu.Unlock()
		c.failPending(fmt.Errorf("lsp client: notification backlog exceeded %d messages", notifyQueueLimit))
		return
	}
	c.notifySeq++
	item.seq = c.notifySeq
	c.notifyQueue = append(c.notifyQueue, item)
	c.notifyMu.Unlock()

	select {
	case c.notifyReady <- struct{}{}:
	default:
		// A wake-up is already pending; the worker drains the whole queue per wake,
		// so this item is covered by it.
	}
}

// NotificationSeq returns the number of notifications received (read off the
// wire and enqueued) so far, including any still waiting to be dispatched to
// the handler. A caller that wants to know whether a notification newer than
// "now" has arrived should snapshot this before triggering whatever produces
// it, then require a subsequently-observed seq to be strictly greater: a
// notification already sitting in the queue at snapshot time has seq <= the
// snapshot, even if the handler doesn't run for it until afterward.
func (c *Client) NotificationSeq() int64 {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	return c.notifySeq
}

func (c *Client) dequeueNotification() (notification, bool) {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	if len(c.notifyQueue) == 0 {
		return notification{}, false
	}
	item := c.notifyQueue[0]
	c.notifyQueue[0] = notification{}
	c.notifyQueue = c.notifyQueue[1:]
	if len(c.notifyQueue) == 0 {
		// Release the backing array once drained; re-slicing alone would keep a
		// burst's worth of capacity (and its already-consumed items) alive.
		c.notifyQueue = nil
	}
	return item, true
}

func (c *Client) deliver(id int64, resp rpcResponse) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- resp
	}
}

func (c *Client) failPending(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.readErr = err
		pending := c.pending
		c.pending = make(map[int64]chan rpcResponse)
		c.mu.Unlock()
		for _, ch := range pending {
			ch <- rpcResponse{Err: &rpcError{Code: -1, Message: err.Error()}}
		}
		close(c.closed)
		c.closeNotifications()
	})
}

// closeNotifications stops accepting notifications and releases anything still
// queued, so a closed client retains nothing. It runs inside closeOnce, after
// c.closed is signaled: an enqueue racing with shutdown therefore either sees
// notifyClosed and drops its item, or appends just before the flag is set and has
// its item cleared here.
//
// Queued-but-unhandled notifications are dropped rather than drained: the worker
// loop is already gone by definition of close, and callers that need diagnostics
// (Manager.Check) collect them before shutting the client down.
func (c *Client) closeNotifications() {
	c.notifyMu.Lock()
	c.notifyClosed = true
	c.notifyQueue = nil
	c.notifyMu.Unlock()
}

func (c *Client) readError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return c.readErr
	}
	return errors.New("lsp client closed")
}

// IsClosed reports whether the client's connection has been torn down — the
// server exited, a read error occurred, or Close was called (all close c.closed).
// A closed client can never serve another request, so the manager evicts and
// restarts its session rather than returning a permanently-dead one.
func (c *Client) IsClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *Client) write(payload any) error {
	// Reject writes once the client is closed so Notify (and Call's request write)
	// can't keep pushing frames onto a dead connection.
	select {
	case <-c.closed:
		return c.readError()
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeMessage(c.writer, payload)
}

// writeMessage frames a JSON-RPC payload with the LSP Content-Length header.
func writeMessage(w io.Writer, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// readMessage reads one LSP-framed message: headers terminated by a blank line,
// then exactly Content-Length bytes of JSON body. Extra headers are ignored.
func readMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid Content-Length %q", value)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, errors.New("message missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}
