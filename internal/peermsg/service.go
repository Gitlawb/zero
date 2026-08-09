package peermsg

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Gitlawb/zero/internal/fsutil"
)

type Handler func(InboundMessage) bool

type StatusHandler func(StatusEvent)

type HeldEvictionHandler func(string)

type HeldReleaseHandler func(InboundMessage)

const (
	peerBucketCapacity    = 30.0
	peerRefillPerSecond   = 0.5
	peerDedupWindow       = 30 * time.Second
	peerMaxSelfHops       = 10
	peerMaxChainLength    = 28
	peerMaxTrackedSenders = 256
	peerMaxHeldMessages   = 100
)

type inboundContextKey struct{}

type senderGuard struct {
	tokens       float64
	lastRefill   time.Time
	lastBody     string
	lastBodyAt   time.Time
	lastActivity time.Time
}

type Options struct {
	RootDir       string
	Identity      Identity
	Now           func() time.Time
	Transport     localTransport
	PID           int
	InboundPolicy InboundPolicy
}

// Service owns one live peer endpoint plus the registry used to discover other
// local Zero sessions. The transport is platform-specific, while framing,
// identity resolution, limits, and delivery policy remain shared.
type Service struct {
	mu              sync.RWMutex
	root            string
	identity        Identity
	now             func() time.Time
	transport       localTransport
	pid             int
	nonce           string
	self            Peer
	listener        net.Listener
	handler         Handler
	statusHandler   StatusHandler
	evictionHandler HeldEvictionHandler
	releaseHandler  HeldReleaseHandler
	policy          InboundPolicy
	outstanding     map[string]Peer
	held            map[string]InboundMessage
	heldOrder       []string
	guards          map[string]*senderGuard
	closed          bool
	wg              sync.WaitGroup
}

func New(options Options) (*Service, error) {
	root := strings.TrimSpace(options.RootDir)
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("peer messaging: resolve runtime directory: %w", err)
	}
	nonce, err := randomHex(8)
	if err != nil {
		return nil, fmt.Errorf("peer messaging: generate endpoint id: %w", err)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	transport := options.Transport
	if transport == nil {
		transport = platformTransport()
	}
	pid := options.PID
	if pid <= 0 {
		pid = os.Getpid()
	}
	return &Service{
		root:        abs,
		identity:    normalizeIdentity(options.Identity),
		now:         now,
		transport:   transport,
		pid:         pid,
		nonce:       nonce,
		policy:      normalizeInboundPolicy(options.InboundPolicy),
		outstanding: make(map[string]Peer),
		held:        make(map[string]InboundMessage),
		guards:      make(map[string]*senderGuard),
	}, nil
}

// WithInboundMessage carries a peer message's relay chain through the agent
// turn so a deliberate send_message reply or forward can extend it. Ordinary
// user turns have no chain and start a fresh one.
func WithInboundMessage(ctx context.Context, message InboundMessage) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	chain := append([]string(nil), message.HopChain...)
	return context.WithValue(ctx, inboundContextKey{}, chain)
}

func DefaultRoot() (string, error) {
	if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
		return filepath.Join(runtimeDir, "zero", "peers"), nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("peer messaging: resolve user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "zero", "peers"), nil
}

func (service *Service) Start(handler Handler) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.listener != nil {
		return nil
	}
	if service.closed {
		return errors.New("peer messaging: service is closed")
	}
	if err := ensurePrivateDir(service.registryDir()); err != nil {
		return fmt.Errorf("peer messaging: create registry: %w", err)
	}
	endpoint, err := service.transport.Endpoint(service.root, service.nonce, service.pid)
	if err != nil {
		return err
	}
	listener, err := service.transport.Listen(endpoint)
	if err != nil {
		return fmt.Errorf("peer messaging: listen: %w", err)
	}
	now := service.now().UTC()
	if service.identity.SessionID == "" {
		service.identity.SessionID = "live-" + service.nonce
	}
	service.self = Peer{
		Identity:  service.identity,
		Endpoint:  endpoint,
		PID:       service.pid,
		StartedAt: now,
		UpdatedAt: now,
		Ref:       peerRef(endpoint),
	}
	service.listener = listener
	service.handler = handler
	if err := service.writeRecordLocked(); err != nil {
		service.listener = nil
		_ = listener.Close()
		_ = service.transport.Remove(endpoint)
		return err
	}
	service.wg.Add(1)
	go service.acceptLoop(listener)
	return nil
}

func (service *Service) SetStatusHandler(handler StatusHandler) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.statusHandler = handler
}

func (service *Service) SetHeldEvictionHandler(handler HeldEvictionHandler) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.evictionHandler = handler
}

func (service *Service) SetHeldReleaseHandler(handler HeldReleaseHandler) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.releaseHandler = handler
}

func (service *Service) Close() error {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil
	}
	service.closed = true
	listener := service.listener
	endpoint := service.self.Endpoint
	held := make([]InboundMessage, 0, len(service.held))
	for _, message := range service.held {
		held = append(held, message)
	}
	service.held = make(map[string]InboundMessage)
	service.listener = nil
	service.mu.Unlock()

	expiryCtx, cancelExpiry := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancelExpiry()
	for _, message := range held {
		if expiryCtx.Err() != nil {
			break
		}
		_ = service.sendStatus(expiryCtx, message, DeliveryExpired)
	}

	var closeErr error
	if listener != nil {
		closeErr = listener.Close()
	}
	service.wg.Wait()
	if endpoint != "" {
		_ = service.transport.Remove(endpoint)
	}
	service.removeOwnRecord(endpoint)
	if errors.Is(closeErr, net.ErrClosed) {
		return nil
	}
	return closeErr
}

// ResolveHeld settles a message that was parked for local approval and sends a
// terminal receipt back to its sender. Repeated or stale decisions are ignored.
func (service *Service) ResolveHeld(ctx context.Context, messageID string, status DeliveryStatus) error {
	if status != DeliveryDelivered && status != DeliveryDenied && status != DeliveryExpired {
		return fmt.Errorf("peer messaging: invalid held-message status %q", status)
	}
	service.mu.Lock()
	message, ok := service.held[messageID]
	if ok {
		delete(service.held, messageID)
		service.removeHeldOrderLocked(messageID)
	}
	service.mu.Unlock()
	if !ok {
		return nil
	}
	return service.sendStatus(ctx, message, status)
}

func (service *Service) UpdateIdentity(identity Identity) error {
	service.mu.Lock()
	service.identity = normalizeIdentity(identity)
	if service.listener == nil {
		service.mu.Unlock()
		return nil
	}
	service.self.Identity = service.identity
	service.self.UpdatedAt = service.now().UTC()
	if err := service.writeRecordLocked(); err != nil {
		service.mu.Unlock()
		return err
	}
	released := make([]InboundMessage, 0)
	if service.policy == InboundPolicyParity && service.releaseHandler != nil {
		for _, messageID := range append([]string(nil), service.heldOrder...) {
			message := service.held[messageID]
			status, _ := service.inboundDecision(message.From.PermissionClass, service.self.PermissionClass)
			if status != DeliveryAccepted {
				continue
			}
			delete(service.held, messageID)
			service.removeHeldOrderLocked(messageID)
			message.RequiresApproval = false
			message.HoldCause = ""
			released = append(released, message)
		}
	}
	releaseHandler := service.releaseHandler
	service.mu.Unlock()
	for _, message := range released {
		if releaseHandler != nil {
			releaseHandler(message)
		}
		go func(message InboundMessage) {
			ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
			defer cancel()
			_ = service.sendStatus(ctx, message, DeliveryDelivered)
		}(message)
	}
	return nil
}

func (service *Service) Self() Peer {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.self
}

func (service *Service) List(ctx context.Context) ([]Peer, error) {
	entries, err := os.ReadDir(service.registryDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("peer messaging: read registry: %w", err)
	}
	self := service.Self()
	peers := make([]Peer, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(service.registryDir(), entry.Name())
		peer, err := readPeerRecord(path)
		if err != nil || peer.Endpoint == "" || peer.Endpoint == self.Endpoint || peer.SessionID == "" {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		conn, dialErr := service.transport.Dial(probeCtx, peer.Endpoint)
		cancel()
		if dialErr != nil {
			service.removeStaleRecord(path, peer.Endpoint)
			continue
		}
		_ = conn.Close()
		peers = append(peers, peer)
	}
	sort.Slice(peers, func(i, j int) bool {
		if strings.EqualFold(peers[i].Name, peers[j].Name) {
			return peers[i].Ref < peers[j].Ref
		}
		return strings.ToLower(peers[i].Name) < strings.ToLower(peers[j].Name)
	})
	return peers, nil
}

func (service *Service) Send(ctx context.Context, to, summary, body string) (SendResult, error) {
	body = normalizeBody(body)
	summary = normalizeSummary(summary)
	if body == "" {
		return SendResult{}, errors.New("peer messaging: message must not be empty")
	}
	if len([]byte(body)) > maxMessageBytes {
		return SendResult{}, fmt.Errorf("peer messaging: message exceeds %d bytes", maxMessageBytes)
	}
	if summary == "" {
		return SendResult{}, errors.New("peer messaging: summary is required")
	}
	peers, err := service.List(ctx)
	if err != nil {
		return SendResult{}, err
	}
	peer, err := resolvePeer(peers, to)
	if err != nil {
		return SendResult{}, err
	}
	id, err := randomHex(16)
	if err != nil {
		return SendResult{}, fmt.Errorf("peer messaging: generate message id: %w", err)
	}
	self := service.Self()
	hopChain := inboundHopChain(ctx)
	if len(hopChain) == 0 || hopChain[len(hopChain)-1] != self.Ref {
		hopChain = append(hopChain, self.Ref)
	}
	if err := validateHopChain(hopChain); err != nil {
		return SendResult{}, err
	}
	frame := sendFrame{
		Version:  ProtocolVersion,
		Type:     "message",
		ID:       id,
		From:     self,
		To:       peer.SessionID,
		Summary:  summary,
		Body:     body,
		HopChain: hopChain,
	}
	service.mu.Lock()
	service.outstanding[id] = peer
	service.mu.Unlock()
	keepOutstanding := false
	defer func() {
		if keepOutstanding {
			return
		}
		service.mu.Lock()
		delete(service.outstanding, id)
		service.mu.Unlock()
	}()
	conn, err := service.transport.Dial(ctx, peer.Endpoint)
	if err != nil {
		return SendResult{}, fmt.Errorf("peer messaging: connect to %s: %w", displayPeer(peer), err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(frame); err != nil {
		return SendResult{}, fmt.Errorf("peer messaging: send to %s: %w", displayPeer(peer), err)
	}
	var response responseFrame
	if err := decodeFrame(conn, &response); err != nil {
		return SendResult{}, fmt.Errorf("peer messaging: receive delivery status from %s: %w", displayPeer(peer), err)
	}
	if response.Version != ProtocolVersion || response.Type != "delivery" || response.ID != id {
		return SendResult{}, errors.New("peer messaging: invalid delivery response")
	}
	if response.Error != "" {
		return SendResult{}, errors.New(response.Error)
	}
	if response.Status != DeliveryAccepted && response.Status != DeliveryHeld && response.Status != DeliveryRefused {
		return SendResult{}, errors.New("peer messaging: invalid delivery status")
	}
	keepOutstanding = response.Status == DeliveryHeld
	return SendResult{MessageID: id, Peer: peer, Status: response.Status}, nil
}

func (service *Service) acceptLoop(listener net.Listener) {
	defer service.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			service.mu.RLock()
			closed := service.closed
			service.mu.RUnlock()
			if closed {
				return
			}
			continue
		}
		service.wg.Add(1)
		go func() {
			defer service.wg.Done()
			defer conn.Close()
			service.handleConn(conn)
		}()
	}
}

func (service *Service) handleConn(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var frame sendFrame
	if err := decodeFrame(conn, &frame); err != nil {
		if !errors.Is(err, io.EOF) {
			_ = json.NewEncoder(conn).Encode(responseFrame{Version: ProtocolVersion, Type: "delivery", Status: DeliveryRefused, Error: "invalid peer message"})
		}
		return
	}
	if frame.Type == "status" {
		service.handleStatusFrame(frame)
		return
	}
	response := responseFrame{Version: ProtocolVersion, Type: "delivery", ID: frame.ID, Status: DeliveryRefused}
	if frame.Version != ProtocolVersion || frame.Type != "message" || frame.ID == "" ||
		frame.From.SessionID == "" || frame.From.Endpoint == "" || frame.From.Ref != peerRef(frame.From.Endpoint) {
		response.Error = "peer messaging: invalid message envelope"
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	if len([]byte(frame.Body)) == 0 || len([]byte(frame.Body)) > maxMessageBytes {
		response.Error = "peer messaging: invalid message size"
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	body := normalizeBody(frame.Body)
	if body == "" {
		response.Error = "peer messaging: invalid message content"
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	service.mu.RLock()
	self := service.self
	handler := service.handler
	service.mu.RUnlock()
	if self.SessionID == "" || frame.To != self.SessionID {
		response.Error = "peer messaging: target session is no longer active"
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	if len(frame.HopChain) == 0 {
		frame.HopChain = []string{frame.From.Ref}
	}
	if err := validateHopChain(frame.HopChain); err != nil || frame.HopChain[len(frame.HopChain)-1] != frame.From.Ref {
		response.Error = "peer messaging: invalid relay chain"
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	if reason := service.admitMessage(frame.From, body, frame.HopChain, self.Ref); reason != "" {
		response.Error = "peer messaging: message dropped: " + reason
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	if handler == nil {
		response.Status = DeliveryRefused
		response.Error = "peer messaging: receiving session is unavailable"
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	status, holdCause := service.inboundDecision(frame.From.PermissionClass, self.PermissionClass)
	response.Status = status
	if status == DeliveryRefused {
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	message := InboundMessage{
		ID:               frame.ID,
		From:             frame.From,
		Body:             body,
		Summary:          normalizeSummary(frame.Summary),
		ReceivedAt:       service.now().UTC(),
		RequiresApproval: status == DeliveryHeld,
		HoldCause:        holdCause,
		HopChain:         append([]string(nil), frame.HopChain...),
	}
	if status == DeliveryHeld {
		var evicted InboundMessage
		var evictionHandler HeldEvictionHandler
		service.mu.Lock()
		if len(service.held) >= peerMaxHeldMessages {
			oldestID := service.heldOrder[0]
			service.heldOrder = service.heldOrder[1:]
			evicted = service.held[oldestID]
			delete(service.held, oldestID)
			evictionHandler = service.evictionHandler
		}
		service.held[message.ID] = message
		service.heldOrder = append(service.heldOrder, message.ID)
		service.mu.Unlock()
		if evicted.ID != "" {
			if evictionHandler != nil {
				evictionHandler(evicted.ID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
			_ = service.sendStatus(ctx, evicted, DeliveryExpired)
			cancel()
		}
	}
	if !handler(message) {
		if status == DeliveryHeld {
			service.mu.Lock()
			delete(service.held, message.ID)
			service.removeHeldOrderLocked(message.ID)
			service.mu.Unlock()
		}
		response.Status = DeliveryRefused
		response.Error = "peer messaging: message dropped: receiver queue is full or unavailable"
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func (service *Service) handleStatusFrame(frame sendFrame) {
	if frame.Version != ProtocolVersion || frame.OrigID == "" || frame.From.SessionID == "" ||
		frame.From.Endpoint == "" || frame.From.Ref != peerRef(frame.From.Endpoint) || !terminalDeliveryStatus(frame.Status) {
		return
	}
	service.mu.Lock()
	self := service.self
	peer, ok := service.outstanding[frame.OrigID]
	if !ok || frame.To != self.SessionID || peer.Endpoint != frame.From.Endpoint || peer.SessionID != frame.From.SessionID {
		service.mu.Unlock()
		return
	}
	delete(service.outstanding, frame.OrigID)
	handler := service.statusHandler
	service.mu.Unlock()
	if handler != nil {
		handler(StatusEvent{MessageID: frame.OrigID, Peer: peer, Status: frame.Status})
	}
}

func (service *Service) sendStatus(ctx context.Context, message InboundMessage, status DeliveryStatus) error {
	if !terminalDeliveryStatus(status) {
		return fmt.Errorf("peer messaging: invalid delivery status %q", status)
	}
	peers, err := service.List(ctx)
	if err != nil {
		return err
	}
	var target Peer
	for _, peer := range peers {
		if peer.Endpoint == message.From.Endpoint && peer.SessionID == message.From.SessionID && peer.Ref == message.From.Ref {
			target = peer
			break
		}
	}
	if target.Endpoint == "" {
		return errors.New("peer messaging: original sender is no longer reachable")
	}
	self := service.Self()
	frame := sendFrame{
		Version: ProtocolVersion,
		Type:    "status",
		From:    self,
		To:      target.SessionID,
		OrigID:  message.ID,
		Status:  status,
	}
	conn, err := service.transport.Dial(ctx, target.Endpoint)
	if err != nil {
		return fmt.Errorf("peer messaging: send status to %s: %w", displayPeer(target), err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(frame); err != nil {
		return fmt.Errorf("peer messaging: send status to %s: %w", displayPeer(target), err)
	}
	return nil
}

func (service *Service) inboundDecision(sender, receiver PermissionClass) (DeliveryStatus, HoldCause) {
	switch service.policy {
	case InboundPolicyAccept:
		return DeliveryAccepted, ""
	case InboundPolicyHold:
		return DeliveryHeld, HoldCauseExplicit
	case InboundPolicyRefuse:
		return DeliveryRefused, ""
	default:
		if sender == "" {
			if receiver == PermissionBypass {
				return DeliveryHeld, HoldCauseModeUnknown
			}
			return DeliveryAccepted, ""
		}
		if sender == receiver {
			return DeliveryAccepted, ""
		}
		return DeliveryHeld, HoldCauseModeMismatch
	}
}

func (service *Service) admitMessage(sender Peer, body string, chain []string, selfRef string) string {
	if len(chain) > peerMaxChainLength {
		return "relay chain is too long"
	}
	selfHops := 0
	for _, ref := range chain {
		if ref == selfRef {
			selfHops++
		}
	}
	if selfHops >= peerMaxSelfHops {
		return "peer messaging loop detected"
	}
	now := service.now()
	key := sender.Endpoint
	service.mu.Lock()
	defer service.mu.Unlock()
	guard := service.guards[key]
	if guard == nil {
		if len(service.guards) >= peerMaxTrackedSenders {
			var oldestKey string
			var oldest time.Time
			for candidate, state := range service.guards {
				if oldestKey == "" || state.lastActivity.Before(oldest) {
					oldestKey, oldest = candidate, state.lastActivity
				}
			}
			delete(service.guards, oldestKey)
		}
		guard = &senderGuard{tokens: peerBucketCapacity, lastRefill: now}
		service.guards[key] = guard
	}
	guard.lastActivity = now
	if guard.lastBody == body && now.Sub(guard.lastBodyAt) < peerDedupWindow {
		return "duplicate of a recent message from this sender"
	}
	elapsed := now.Sub(guard.lastRefill).Seconds()
	if elapsed > 0 {
		guard.tokens = min(peerBucketCapacity, guard.tokens+elapsed*peerRefillPerSecond)
		guard.lastRefill = now
	}
	if guard.tokens < 1 {
		return "sender exceeded the peer message rate limit"
	}
	guard.tokens--
	guard.lastBody = body
	guard.lastBodyAt = now
	return ""
}

func (service *Service) removeHeldOrderLocked(messageID string) {
	for index, candidate := range service.heldOrder {
		if candidate == messageID {
			service.heldOrder = append(service.heldOrder[:index], service.heldOrder[index+1:]...)
			return
		}
	}
}

func inboundHopChain(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	chain, _ := ctx.Value(inboundContextKey{}).([]string)
	return append([]string(nil), chain...)
}

func validateHopChain(chain []string) error {
	if len(chain) == 0 || len(chain) > peerMaxChainLength {
		return errors.New("peer messaging: invalid relay chain")
	}
	for _, ref := range chain {
		if len(ref) != 8 {
			return errors.New("peer messaging: invalid relay chain")
		}
		if _, err := hex.DecodeString(ref); err != nil {
			return errors.New("peer messaging: invalid relay chain")
		}
	}
	return nil
}

func terminalDeliveryStatus(status DeliveryStatus) bool {
	return status == DeliveryDelivered || status == DeliveryDenied || status == DeliveryExpired
}

func normalizeInboundPolicy(policy InboundPolicy) InboundPolicy {
	switch policy {
	case InboundPolicyAccept, InboundPolicyHold, InboundPolicyRefuse:
		return policy
	default:
		return InboundPolicyParity
	}
}

func decodeFrame(reader io.Reader, target any) error {
	buffered := bufio.NewReader(io.LimitReader(reader, maxFrameBytes+1))
	line, err := buffered.ReadBytes('\n')
	if err != nil {
		return err
	}
	if len(line) > maxFrameBytes {
		return errors.New("peer messaging: frame is too large")
	}
	if err := json.Unmarshal(line, target); err != nil {
		return fmt.Errorf("peer messaging: decode frame: %w", err)
	}
	return nil
}

func (service *Service) registryDir() string { return filepath.Join(service.root, "registry") }

func (service *Service) recordPath() string {
	return filepath.Join(service.registryDir(), fmt.Sprintf("%d-%s.json", service.pid, service.nonce))
}

func (service *Service) writeRecordLocked() error {
	data, err := json.Marshal(service.self)
	if err != nil {
		return fmt.Errorf("peer messaging: encode registry record: %w", err)
	}
	path := service.recordPath()
	tmp, err := os.CreateTemp(service.registryDir(), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("peer messaging: create registry temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fsutil.RenameWithRetry(tmpPath, path, nil); err != nil {
		return fmt.Errorf("peer messaging: publish registry record: %w", err)
	}
	return nil
}

func readPeerRecord(path string) (Peer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Peer{}, err
	}
	var peer Peer
	if err := json.Unmarshal(data, &peer); err != nil {
		return Peer{}, err
	}
	if peer.PID <= 0 || peer.Endpoint == "" || peer.Ref != peerRef(peer.Endpoint) {
		return Peer{}, errors.New("peer messaging: invalid registry record")
	}
	peer.Identity = normalizeIdentity(peer.Identity)
	return peer, nil
}

func (service *Service) removeOwnRecord(endpoint string) {
	path := service.recordPath()
	peer, err := readPeerRecord(path)
	if err == nil && peer.Endpoint == endpoint {
		_ = os.Remove(path)
	}
}

func (service *Service) removeStaleRecord(path, endpoint string) {
	peer, err := readPeerRecord(path)
	if err == nil && peer.Endpoint == endpoint {
		_ = os.Remove(path)
	}
}

func resolvePeer(peers []Peer, target string) (Peer, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return Peer{}, errors.New("peer messaging: recipient must not be empty")
	}
	for _, peer := range peers {
		if peer.SessionID == target || strings.EqualFold(displayPeer(peer), target) {
			return peer, nil
		}
	}
	matches := make([]Peer, 0, 2)
	for _, peer := range peers {
		if strings.EqualFold(peer.Name, target) {
			matches = append(matches, peer)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Peer{}, fmt.Errorf("peer messaging: %q is ambiguous; use name [ref] from list_sessions", target)
	}
	return Peer{}, fmt.Errorf("peer messaging: no reachable session named %q", target)
}

func displayPeer(peer Peer) string {
	name := strings.TrimSpace(peer.Name)
	if name == "" {
		name = "Zero session"
	}
	return fmt.Sprintf("%s [%s]", name, peer.Ref)
}

func normalizeIdentity(identity Identity) Identity {
	identity.SessionID = truncateRunes(strings.TrimSpace(sanitizePlainText(identity.SessionID, false)), 256)
	identity.Name = truncateRunes(strings.TrimSpace(sanitizePlainText(identity.Name, false)), 80)
	identity.Cwd = truncateRunes(strings.TrimSpace(sanitizePlainText(identity.Cwd, false)), 4096)
	switch identity.PermissionClass {
	case PermissionPrompting, PermissionBypass:
	default:
		identity.PermissionClass = ""
	}
	return identity
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func normalizeSummary(summary string) string {
	summary = strings.Split(summary, "\n")[0]
	summary = strings.TrimSpace(sanitizePlainText(summary, false))
	runes := []rune(summary)
	if len(runes) > 200 {
		summary = string(runes[:200])
	}
	return summary
}

func normalizeBody(body string) string {
	return strings.TrimSpace(sanitizePlainText(body, true))
}

func sanitizePlainText(value string, multiline bool) string {
	return strings.Map(func(char rune) rune {
		if multiline && (char == '\n' || char == '\t') {
			return char
		}
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, value)
}

func peerRef(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:4])
}

func randomHex(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
