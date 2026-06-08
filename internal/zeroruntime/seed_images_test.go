package zeroruntime

import "testing"

func TestSeedMessagesWithImagesThreadsImagesOntoUserTurn(t *testing.T) {
	images := []ImageBlock{
		{MediaType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
		{MediaType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
	}
	messages := SeedMessagesWithImages("you are a helper", "describe these", images)

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Role != MessageRoleSystem || messages[0].Content != "you are a helper" {
		t.Fatalf("unexpected system message: %#v", messages[0])
	}
	if messages[0].Images != nil {
		t.Fatalf("system message must not carry images, got %#v", messages[0].Images)
	}
	if messages[1].Role != MessageRoleUser || messages[1].Content != "describe these" {
		t.Fatalf("unexpected user message: %#v", messages[1])
	}
	if len(messages[1].Images) != 2 {
		t.Fatalf("expected 2 images on the user turn, got %d", len(messages[1].Images))
	}
	if messages[1].Images[0].MediaType != "image/png" || messages[1].Images[1].MediaType != "image/jpeg" {
		t.Fatalf("images not threaded in order: %#v", messages[1].Images)
	}
}

func TestSeedMessagesDelegatesWithNilImages(t *testing.T) {
	// SeedMessages keeps its (system, user) signature and must leave Images nil
	// so the text-only path is byte-identical to before.
	messages := SeedMessages("sys", "usr")

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[1].Role != MessageRoleUser || messages[1].Content != "usr" {
		t.Fatalf("unexpected user message: %#v", messages[1])
	}
	if messages[0].Images != nil || messages[1].Images != nil {
		t.Fatalf("SeedMessages must leave Images nil, got %#v / %#v", messages[0].Images, messages[1].Images)
	}
}
