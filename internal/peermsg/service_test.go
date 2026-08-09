package peermsg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServicesDiscoverAndDeliverAcceptedMessage(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	receiver := newStartedService(t, root, transport, 9102, Identity{
		SessionID:       "receiver-session",
		Name:            "reviewer",
		Cwd:             root,
		PermissionClass: PermissionPrompting,
	})
	sender := newStartedService(t, root, transport, 9101, Identity{
		SessionID:       "sender-session",
		Name:            "builder",
		Cwd:             root,
		PermissionClass: PermissionPrompting,
	})

	received := make(chan InboundMessage, 1)
	receiver.mu.Lock()
	receiver.handler = func(message InboundMessage) bool { received <- message; return true }
	receiver.mu.Unlock()

	peers, err := sender.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].SessionID != "receiver-session" || peers[0].Ref == "" {
		t.Fatalf("peers = %#v", peers)
	}

	result, err := sender.Send(context.Background(), "reviewer", "check status", "Are the tests green?")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != DeliveryAccepted || result.Peer.SessionID != "receiver-session" || result.MessageID == "" {
		t.Fatalf("result = %#v", result)
	}
	select {
	case message := <-received:
		if message.Body != "Are the tests green?" || message.Summary != "check status" || message.RequiresApproval {
			t.Fatalf("message = %#v", message)
		}
		if message.From.SessionID != "sender-session" {
			t.Fatalf("from = %#v", message.From)
		}
		if len(message.HopChain) != 1 || message.HopChain[0] != sender.Self().Ref {
			t.Fatalf("hop chain = %#v", message.HopChain)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestFreshSessionIsDiscoverableBeforeItsFirstPrompt(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	receiver := newStartedService(t, root, transport, 9152, Identity{Name: ""})
	sender := newStartedService(t, root, transport, 9151, Identity{SessionID: "sender", Name: "sender"})
	peers, err := sender.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || !strings.HasPrefix(peers[0].SessionID, "live-") || displayPeer(peers[0]) != "Zero session ["+peers[0].Ref+"]" {
		t.Fatalf("fresh peer = %#v", peers)
	}
	if receiver.Self().SessionID != peers[0].SessionID {
		t.Fatalf("receiver identity = %#v, discovered = %#v", receiver.Self(), peers[0])
	}
}

func TestPermissionClassMismatchHoldsMessage(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	received := make(chan InboundMessage, 1)
	receiver := newService(t, root, transport, 9202, Identity{SessionID: "receiver", Name: "safe", PermissionClass: PermissionPrompting})
	if err := receiver.Start(func(message InboundMessage) bool { received <- message; return true }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	sender := newStartedService(t, root, transport, 9201, Identity{SessionID: "sender", Name: "unsafe", PermissionClass: PermissionBypass})

	result, err := sender.Send(context.Background(), "safe", "request", "run this")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != DeliveryHeld {
		t.Fatalf("status = %q", result.Status)
	}
	select {
	case message := <-received:
		if !message.RequiresApproval || message.HoldCause != HoldCauseModeMismatch {
			t.Fatalf("message should require approval: %#v", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for held message")
	}
}

func TestExplicitInboundPoliciesOverridePermissionParity(t *testing.T) {
	tests := []struct {
		name       string
		policy     InboundPolicy
		senderMode PermissionClass
		want       DeliveryStatus
		wantCause  HoldCause
		wantRecv   bool
	}{
		{name: "accept mismatch", policy: InboundPolicyAccept, senderMode: PermissionBypass, want: DeliveryAccepted, wantRecv: true},
		{name: "hold match", policy: InboundPolicyHold, senderMode: PermissionPrompting, want: DeliveryHeld, wantCause: HoldCauseExplicit, wantRecv: true},
		{name: "refuse match", policy: InboundPolicyRefuse, senderMode: PermissionPrompting, want: DeliveryRefused},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			transport := newMemoryTransport()
			receiver, err := New(Options{
				RootDir: root, PID: 9602, Transport: transport, InboundPolicy: test.policy,
				Identity: Identity{SessionID: "receiver", Name: "receiver", PermissionClass: PermissionPrompting},
			})
			if err != nil {
				t.Fatal(err)
			}
			received := make(chan InboundMessage, 1)
			if err := receiver.Start(func(message InboundMessage) bool { received <- message; return true }); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = receiver.Close() })
			sender := newStartedService(t, root, transport, 9601, Identity{SessionID: "sender", Name: "sender", PermissionClass: test.senderMode})

			result, err := sender.Send(context.Background(), "receiver", "policy", "hello")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want {
				t.Fatalf("status = %q, want %q", result.Status, test.want)
			}
			select {
			case message := <-received:
				if !test.wantRecv {
					t.Fatalf("refused message delivered: %#v", message)
				}
				if message.HoldCause != test.wantCause {
					t.Fatalf("hold cause = %q, want %q", message.HoldCause, test.wantCause)
				}
			default:
				if test.wantRecv {
					t.Fatal("message was not delivered to handler")
				}
			}
		})
	}
}

func TestUnknownSenderPermissionModeHoldsOnlyForBypassReceiver(t *testing.T) {
	tests := []struct {
		name         string
		receiverMode PermissionClass
		wantStatus   DeliveryStatus
		wantCause    HoldCause
	}{
		{name: "prompting receiver accepts", receiverMode: PermissionPrompting, wantStatus: DeliveryAccepted},
		{name: "bypass receiver holds", receiverMode: PermissionBypass, wantStatus: DeliveryHeld, wantCause: HoldCauseModeUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{policy: InboundPolicyParity}
			status, cause := service.inboundDecision("", test.receiverMode)
			if status != test.wantStatus || cause != test.wantCause {
				t.Fatalf("inboundDecision() = (%q, %q), want (%q, %q)", status, cause, test.wantStatus, test.wantCause)
			}
		})
	}
}

func TestHeldMessageResolutionSendsTerminalReceipt(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	received := make(chan InboundMessage, 1)
	receiver := newService(t, root, transport, 9702, Identity{SessionID: "receiver", Name: "reviewer", PermissionClass: PermissionPrompting})
	if err := receiver.Start(func(message InboundMessage) bool { received <- message; return true }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	sender := newStartedService(t, root, transport, 9701, Identity{SessionID: "sender", Name: "builder", PermissionClass: PermissionBypass})
	statuses := make(chan StatusEvent, 1)
	sender.SetStatusHandler(func(event StatusEvent) { statuses <- event })

	result, err := sender.Send(context.Background(), "reviewer", "question", "How many files?")
	if err != nil || result.Status != DeliveryHeld {
		t.Fatalf("send result = %#v, err = %v", result, err)
	}
	message := <-received
	if err := receiver.ResolveHeld(context.Background(), message.ID, DeliveryDelivered); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-statuses:
		if event.MessageID != result.MessageID || event.Status != DeliveryDelivered || event.Peer.SessionID != "receiver" {
			t.Fatalf("status event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delivery receipt")
	}
}

func TestPermissionModeChangeReleasesNewlyCompatibleHeldMessage(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	held := make(chan InboundMessage, 1)
	released := make(chan InboundMessage, 1)
	receiver := newService(t, root, transport, 9752, Identity{SessionID: "receiver", Name: "reviewer", PermissionClass: PermissionPrompting})
	receiver.SetHeldReleaseHandler(func(message InboundMessage) { released <- message })
	if err := receiver.Start(func(message InboundMessage) bool { held <- message; return true }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	sender := newStartedService(t, root, transport, 9751, Identity{SessionID: "sender", Name: "builder", PermissionClass: PermissionBypass})
	statuses := make(chan StatusEvent, 1)
	sender.SetStatusHandler(func(event StatusEvent) { statuses <- event })

	result, err := sender.Send(context.Background(), "reviewer", "question", "How many files?")
	if err != nil || result.Status != DeliveryHeld {
		t.Fatalf("send result = %#v, err = %v", result, err)
	}
	message := <-held
	if err := receiver.UpdateIdentity(Identity{SessionID: "receiver", Name: "reviewer", PermissionClass: PermissionBypass}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-released:
		if got.ID != message.ID || got.RequiresApproval || got.HoldCause != "" {
			t.Fatalf("released message = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for held message release")
	}
	select {
	case event := <-statuses:
		if event.MessageID != result.MessageID || event.Status != DeliveryDelivered {
			t.Fatalf("status event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for release receipt")
	}
}

func TestReceiverAdmissionAndLoopGuardsFailClosed(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	receiver := newService(t, root, transport, 9802, Identity{SessionID: "receiver", Name: "receiver"})
	if err := receiver.Start(func(InboundMessage) bool { return false }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	sender := newStartedService(t, root, transport, 9801, Identity{SessionID: "sender", Name: "sender"})
	if _, err := sender.Send(context.Background(), "receiver", "full", "not admitted"); err == nil || !strings.Contains(err.Error(), "receiver queue") {
		t.Fatalf("admission error = %v", err)
	}

	receiver.mu.Lock()
	receiver.handler = func(InboundMessage) bool { return true }
	receiver.mu.Unlock()
	if _, err := sender.Send(context.Background(), "receiver", "once", "duplicate body"); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Send(context.Background(), "receiver", "twice", "duplicate body"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}

	chain := make([]string, peerMaxSelfHops)
	for index := range chain {
		chain[index] = receiver.Self().Ref
	}
	ctx := context.WithValue(context.Background(), inboundContextKey{}, chain)
	if _, err := sender.Send(ctx, "receiver", "loop", "different body"); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("loop error = %v", err)
	}
}

func TestUntitledPeerAddressResolvesByNeutralNameAndRef(t *testing.T) {
	peer := Peer{Identity: Identity{SessionID: "zero_opaque"}, Ref: "22222222"}
	resolved, err := resolvePeer([]Peer{peer}, "Zero session [22222222]")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SessionID != peer.SessionID {
		t.Fatalf("resolved peer = %#v", resolved)
	}
}

func TestIdentityUpdateChangesAddressableSession(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	receiver := newStartedService(t, root, transport, 9302, Identity{SessionID: "old", Name: "old name"})
	sender := newStartedService(t, root, transport, 9301, Identity{SessionID: "sender", Name: "sender"})

	if err := receiver.UpdateIdentity(Identity{SessionID: "new", Name: "new name"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Send(context.Background(), "old", "stale", "hello"); err == nil || !strings.Contains(err.Error(), "no reachable") {
		t.Fatalf("old identity error = %v", err)
	}
	result, err := sender.Send(context.Background(), "new name", "fresh", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Peer.SessionID != "new" {
		t.Fatalf("peer = %#v", result.Peer)
	}
}

func TestClosedServiceDisappearsFromDiscovery(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	receiver := newStartedService(t, root, transport, 9402, Identity{SessionID: "receiver", Name: "receiver"})
	sender := newStartedService(t, root, transport, 9401, Identity{SessionID: "sender", Name: "sender"})
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	peers, err := sender.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 0 {
		t.Fatalf("peers after close = %#v", peers)
	}
}

func TestReceiverRefusesForgedSenderReference(t *testing.T) {
	root := t.TempDir()
	transport := newMemoryTransport()
	received := make(chan InboundMessage, 1)
	receiver := newService(t, root, transport, 9502, Identity{SessionID: "receiver", Name: "receiver"})
	if err := receiver.Start(func(message InboundMessage) bool { received <- message; return true }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close() })

	conn, err := transport.Dial(context.Background(), receiver.Self().Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	frame := sendFrame{
		Version: ProtocolVersion,
		Type:    "message",
		ID:      "forged-message",
		From: Peer{
			Identity: Identity{SessionID: "sender", Name: "sender"},
			Endpoint: "memory:sender",
			Ref:      "forged",
		},
		To:      "receiver",
		Summary: "hello",
		Body:    "This must not be delivered.",
	}
	if err := json.NewEncoder(conn).Encode(frame); err != nil {
		t.Fatal(err)
	}
	var response responseFrame
	if err := decodeFrame(conn, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != DeliveryRefused || !strings.Contains(response.Error, "invalid message envelope") {
		t.Fatalf("response = %#v", response)
	}
	select {
	case message := <-received:
		t.Fatalf("forged message was delivered: %#v", message)
	default:
	}
}

func TestResolvePeerRequiresReferenceForDuplicateNames(t *testing.T) {
	peers := []Peer{
		{Identity: Identity{SessionID: "one", Name: "worker"}, Ref: "11111111"},
		{Identity: Identity{SessionID: "two", Name: "worker"}, Ref: "22222222"},
	}
	if _, err := resolvePeer(peers, "worker"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous resolution error = %v", err)
	}
	peer, err := resolvePeer(peers, "worker [22222222]")
	if err != nil {
		t.Fatal(err)
	}
	if peer.SessionID != "two" {
		t.Fatalf("peer = %#v", peer)
	}
}

func TestPlainTextNormalizationRemovesTerminalControls(t *testing.T) {
	if got := normalizeBody("  hello\x1b[31m\nworld\x00  "); got != "hello[31m\nworld" {
		t.Fatalf("normalized body = %q", got)
	}
	if got := normalizeSummary("  status\x1b[31m\nignored  "); got != "status[31m" {
		t.Fatalf("normalized summary = %q", got)
	}
}

func newStartedService(t *testing.T, root string, transport localTransport, pid int, identity Identity) *Service {
	t.Helper()
	service := newService(t, root, transport, pid, identity)
	if err := service.Start(func(InboundMessage) bool { return true }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func newService(t *testing.T, root string, transport localTransport, pid int, identity Identity) *Service {
	t.Helper()
	service, err := New(Options{RootDir: root, PID: pid, Identity: identity, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type memoryTransport struct {
	mu        sync.Mutex
	listeners map[string]*memoryListener
}

func newMemoryTransport() *memoryTransport {
	return &memoryTransport{listeners: map[string]*memoryListener{}}
}

func (transport *memoryTransport) Endpoint(_ string, nonce string, pid int) (string, error) {
	return fmt.Sprintf("memory:%d:%s", pid, nonce), nil
}

func (transport *memoryTransport) Listen(endpoint string) (net.Listener, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if _, exists := transport.listeners[endpoint]; exists {
		return nil, errors.New("already listening")
	}
	listener := &memoryListener{endpoint: endpoint, conns: make(chan net.Conn), done: make(chan struct{})}
	transport.listeners[endpoint] = listener
	return listener, nil
}

func (transport *memoryTransport) Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	transport.mu.Lock()
	listener := transport.listeners[endpoint]
	transport.mu.Unlock()
	if listener == nil {
		return nil, errors.New("not listening")
	}
	client, server := net.Pipe()
	select {
	case listener.conns <- server:
		return client, nil
	case <-listener.done:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

func (transport *memoryTransport) Remove(endpoint string) error {
	transport.mu.Lock()
	delete(transport.listeners, endpoint)
	transport.mu.Unlock()
	return nil
}

type memoryListener struct {
	endpoint string
	conns    chan net.Conn
	done     chan struct{}
	once     sync.Once
}

func (listener *memoryListener) Accept() (net.Conn, error) {
	select {
	case conn := <-listener.conns:
		return conn, nil
	case <-listener.done:
		return nil, net.ErrClosed
	}
}

func (listener *memoryListener) Close() error {
	listener.once.Do(func() { close(listener.done) })
	return nil
}

func (listener *memoryListener) Addr() net.Addr { return memoryAddr(listener.endpoint) }

type memoryAddr string

func (address memoryAddr) Network() string { return "memory" }
func (address memoryAddr) String() string  { return string(address) }
