package agent

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// imageEchoProvider records the messages of the first request it receives, then
// returns an empty final answer so the loop terminates after one turn.
type imageEchoProvider struct {
	seen []zeroruntime.Message
}

func (p *imageEchoProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	if p.seen == nil {
		p.seen = append([]zeroruntime.Message{}, request.Messages...)
	}
	events := make(chan zeroruntime.StreamEvent)
	close(events)
	return events, nil
}

func TestRunSeedsImagesIntoUserTurn(t *testing.T) {
	provider := &imageEchoProvider{}
	images := []zeroruntime.ImageBlock{{MediaType: "image/png", Data: []byte{0x89, 0x50}}}

	if _, err := Run(context.Background(), "look at this", provider, Options{
		MaxTurns: 1,
		Images:   images,
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(provider.seen) < 2 {
		t.Fatalf("provider saw %d messages, want >= 2", len(provider.seen))
	}
	user := provider.seen[len(provider.seen)-1]
	if user.Role != zeroruntime.MessageRoleUser {
		t.Fatalf("last seeded message role = %q, want user", user.Role)
	}
	if len(user.Images) != 1 || user.Images[0].MediaType != "image/png" {
		t.Fatalf("user.Images = %#v, want one image/png block", user.Images)
	}
}

func TestRunWithoutImagesSeedsNilImages(t *testing.T) {
	provider := &imageEchoProvider{}
	if _, err := Run(context.Background(), "hello", provider, Options{MaxTurns: 1}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	user := provider.seen[len(provider.seen)-1]
	if user.Images != nil {
		t.Fatalf("user.Images = %#v, want nil for text-only run", user.Images)
	}
}
