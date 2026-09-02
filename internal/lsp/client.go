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
// notification's receipt sequence (see Client.ReceiptSeq): the position of the
// frame it arrived in, stamped the moment that frame came off the wire — NOT
// when this handler happens to run, which can lag receipt when the queue is
// backed up. A caller that needs to know whether something newer than a given
// point has arrived must compare against seq, not against when its own handling
// code runs.
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
	notifyBytes  int
	notifyReady  chan struct{}
	notifyClosed bool
	// notificationDrained is closed by notificationLoop, and only after
	// closeNotifications has stopped admission and every accepted notification
	// (including an in-flight handler) has finished.
	notificationDrained chan struct{}
	receiptSeq          int64 // frames read off the wire so far (see ReceiptSeq)
	// acceptedSeq is the receipt seq of the last notification queued for the
	// worker, handledSeq the seq of the last one whose handler has returned, and
	// handledWait a channel closed (and replaced) each time handledSeq advances.
	// Together they let a waiter tell whether the worker has caught up with what
	// the reader accepted. Both track notifications only; receiptSeq counts every
	// frame, so the two are compared against each other, never against it.
	acceptedSeq int64
	handledSeq  int64
	handledWait chan struct{}
}

type notification struct {
	method string
	params json.RawMessage
	seq    int64
}

// The notification backlog is bounded by both message count and retained
// payload bytes. A well-behaved handler drains far faster than any single burst
// fills either limit; sustained overload — a language server emitting faster
// than the single handler can consume, or a handler stuck waiting on a
// re-entrant Call — is a fatal condition for this client, not something to
// paper over by growing the queue without bound. Hitting either limit fails the
// client (see enqueueNotification): IsClosed becomes true, and the manager
// evicts and restarts the session on next use, exactly as it does for any other
// dead client.
const (
	notifyQueueLimit     = 4096
	notifyQueueByteLimit = 16 << 20 // 16 MiB
)

// maxFrameBytes is the largest single frame this transport will read. Without
// it the byte budget above would not be authoritative: readMessage allocates the
// whole body before anything can classify it, so one notification with a huge
// Content-Length would be materialized in full and only then rejected by
// enqueueNotification. Capping at the same value keeps that impossible — a
// notification can never allocate past the budget it is measured against — and
// bounds what a malformed or hostile header can make this client allocate. A
// legitimate LSP frame is orders of magnitude smaller; one that is not is a
// protocol error, and failing the read closes the client so the manager restarts
// the session.
const maxFrameBytes = notifyQueueByteLimit

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
		writer:              w,
		pending:             make(map[int64]chan rpcResponse),
		closed:              make(chan struct{}),
		notifyReady:         make(chan struct{}, 1),
		notificationDrained: make(chan struct{}),
		handledWait:         make(chan struct{}),
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
		// Stamp the receipt boundary here — while the frame is nothing but bytes
		// that have left the wire — rather than at enqueue. Everything below
		// (json.Unmarshal of a peer-sized payload, then enqueue) runs on this
		// goroutine while another can be capturing a baseline from ReceiptSeq, so
		// stamping later would let a publish that was already read be numbered
		// above a baseline taken after it arrived, and a superseded publish would
		// then look fresh. Frames that turn out not to be notifications consume a
		// number too: the sequence is a receipt clock, and gaps in it are fine.
		seq := c.stampReceipt()
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
			c.enqueueNotification(notification{method: msg.Method, params: msg.Params, seq: seq})
		case hasID:
			var id int64
			if err := json.Unmarshal(msg.ID, &id); err == nil {
				c.deliver(id, rpcResponse{Result: msg.Result, Err: msg.Error})
			}
		}
	}
}

func (c *Client) notificationLoop() {
	defer close(c.notificationDrained)
	for {
		<-c.notifyReady
		for {
			notification, ok, closed := c.dequeueNotification()
			if !ok {
				if closed {
					return
				}
				break
			}
			c.mu.Lock()
			handler := c.handler
			c.mu.Unlock()
			if handler != nil {
				handler(notification.method, notification.params, notification.seq)
			}
			c.markHandled(notification.seq)
		}
	}
}

// markHandled records that everything received through seq has now been handled
// and wakes anyone waiting for the worker to catch up.
func (c *Client) markHandled(seq int64) {
	c.notifyMu.Lock()
	if seq > c.handledSeq {
		c.handledSeq = seq
	}
	wait := c.handledWait
	c.handledWait = make(chan struct{})
	c.notifyMu.Unlock()
	if wait != nil {
		close(wait)
	}
}

// handledThrough returns the receipt seq the worker has finished handling, plus
// a channel closed the next time that advances. The channel is nil for a Client
// built without NewClient (some unit tests), which has no worker to advance it.
func (c *Client) handledThrough() (int64, <-chan struct{}) {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	return c.handledSeq, c.handledWait
}

// acceptedNotificationSeq returns the receipt seq of the most recently queued
// notification (0 if none). It is the point the worker must reach to have
// handled everything the reader had accepted at the time of the call.
func (c *Client) acceptedNotificationSeq() int64 {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	return c.acceptedSeq
}

// notificationsDone returns the worker completion signal. A nil result means
// this Client was constructed without NewClient (as some unit-test clients are)
// and has no notification worker to wait for.
func (c *Client) notificationsDone() <-chan struct{} {
	return c.notificationDrained
}

// enqueueNotification hands a server notification to the worker loop. item.seq
// must already carry the receipt stamp from stampReceipt: the receipt boundary
// belongs to the moment the frame left the wire, not to this call, which happens
// a full json.Unmarshal later. It never blocks and never silently discards a
// message the handler could still act on: the queue grows instead, up to its
// message and byte limits.
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
// out. The count limit alone is insufficient because notification payloads are
// peer-controlled and can be large; the byte limit prevents a few large
// diagnostics publishes from exhausting the heap before the count limit is
// reached. Either limit turns overload into an explicit, observable failure —
// the client is failed and closed — rather than unbounded protocol retention.
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
	itemBytes := len(item.method) + len(item.params)
	if len(c.notifyQueue) >= notifyQueueLimit || itemBytes > notifyQueueByteLimit-c.notifyBytes {
		c.notifyMu.Unlock()
		c.failPending(fmt.Errorf(
			"lsp client: notification backlog exceeded %d messages or %d bytes",
			notifyQueueLimit,
			notifyQueueByteLimit,
		))
		return
	}
	c.notifyQueue = append(c.notifyQueue, item)
	c.notifyBytes += itemBytes
	c.acceptedSeq = item.seq
	c.notifyMu.Unlock()

	select {
	case c.notifyReady <- struct{}{}:
	default:
		// A wake-up is already pending; the worker drains the whole queue per wake,
		// so this item is covered by it.
	}
}

// stampReceipt claims the next receipt number for a frame that has just been
// read off the wire. The read loop calls it for every frame before parsing one,
// so a notification's seq reflects when it arrived rather than when this client
// got around to decoding and queueing it.
func (c *Client) stampReceipt() int64 {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	c.receiptSeq++
	return c.receiptSeq
}

// ReceiptSeq returns how many frames have been read off the wire so far,
// including any notification still waiting to be dispatched to the handler. A
// caller that wants to know whether a notification newer than "now" has arrived
// should snapshot this before triggering whatever produces it, then require a
// subsequently-observed seq to be strictly greater: a notification whose frame
// had already been read at snapshot time has seq <= the snapshot, even if it is
// still queued and its handler doesn't run until afterward.
func (c *Client) ReceiptSeq() int64 {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	return c.receiptSeq
}

func (c *Client) dequeueNotification() (notification, bool, bool) {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	if len(c.notifyQueue) == 0 {
		return notification{}, false, c.notifyClosed
	}
	item := c.notifyQueue[0]
	c.notifyBytes -= len(item.method) + len(item.params)
	c.notifyQueue[0] = notification{}
	c.notifyQueue = c.notifyQueue[1:]
	if len(c.notifyQueue) == 0 {
		// Release the backing array once drained; re-slicing alone would keep a
		// burst's worth of capacity (and its already-consumed items) alive.
		c.notifyQueue = nil
	}
	return item, true, false
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

// closeNotifications atomically stops accepting notifications, then wakes the
// worker so everything accepted before that boundary is delivered in FIFO order.
// It deliberately does not wait: a user handler may be blocked, and transport
// failure, overload, and concurrent Close must never deadlock teardown. The
// worker exits itself after the accepted queue (including any in-flight handler)
// has drained. An enqueue racing with shutdown is therefore either accepted and
// delivered before worker exit, or observes notifyClosed and is rejected.
func (c *Client) closeNotifications() {
	c.notifyMu.Lock()
	c.notifyClosed = true
	c.notifyMu.Unlock()
	select {
	case c.notifyReady <- struct{}{}:
	default:
	}
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
	if contentLength > maxFrameBytes {
		return nil, fmt.Errorf("lsp frame of %d bytes exceeds the %d byte limit", contentLength, maxFrameBytes)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}
