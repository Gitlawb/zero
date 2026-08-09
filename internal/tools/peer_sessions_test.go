package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/peermsg"
)

type fakePeerSessionService struct {
	peers   []peermsg.Peer
	sentTo  string
	summary string
	body    string
	result  peermsg.SendResult
	err     error
}

func (service *fakePeerSessionService) List(context.Context) ([]peermsg.Peer, error) {
	return service.peers, service.err
}

func (service *fakePeerSessionService) Send(_ context.Context, to, summary, body string) (peermsg.SendResult, error) {
	service.sentTo = to
	service.summary = summary
	service.body = body
	return service.result, service.err
}

func TestListSessionsToolFormatsAddressWithoutTransportDetails(t *testing.T) {
	service := &fakePeerSessionService{peers: []peermsg.Peer{{
		Identity: peermsg.Identity{SessionID: "session-1", Name: "reviewer", Cwd: "/work"},
		Endpoint: "private-endpoint",
		PID:      123,
		Ref:      "a1b2c3d4",
	}}}
	toolset := NewPeerSessionTools(service)
	result := toolset[0].Run(context.Background(), map[string]any{})
	if result.Status != StatusOK || !strings.Contains(result.Output, "reviewer [a1b2c3d4]") {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Output, "private-endpoint") || strings.Contains(result.Output, "123") {
		t.Fatalf("transport internals leaked: %q", result.Output)
	}
}

func TestSendMessageToolPassesPlainTextEnvelope(t *testing.T) {
	service := &fakePeerSessionService{result: peermsg.SendResult{
		MessageID: "m1",
		Status:    peermsg.DeliveryHeld,
		Peer:      peermsg.Peer{Identity: peermsg.Identity{SessionID: "s1", Name: "reviewer"}, Ref: "abcd1234"},
	}}
	toolset := NewPeerSessionTools(service)
	result := toolset[1].Run(context.Background(), map[string]any{
		"to": "reviewer", "summary": "review request", "message": "Please inspect this.",
	})
	if result.Status != StatusOK || result.Meta["status"] != "held" {
		t.Fatalf("result = %#v", result)
	}
	if service.sentTo != "reviewer" || service.summary != "review request" || service.body != "Please inspect this." {
		t.Fatalf("sent = %q %q %q", service.sentTo, service.summary, service.body)
	}
}

func TestPeerReplyToolIsEagerOnlyForPeerAwareTurns(t *testing.T) {
	service := &fakePeerSessionService{}
	ordinary := NewPeerSessionTools(service)
	if len(ordinary) != 2 || !IsDeferred(ordinary[1]) {
		t.Fatalf("ordinary send_message should remain deferred: %#v", ordinary)
	}
	reply := NewPeerReplyTool(service)
	if reply == nil || IsDeferred(reply) || reply.Name() != "send_message" {
		t.Fatalf("peer reply tool should be eager: %#v", reply)
	}
}
