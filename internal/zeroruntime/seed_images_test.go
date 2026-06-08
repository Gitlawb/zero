package zeroruntime

import (
	"bytes"
	"testing"
)

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

// TestSeedMessagesWithImagesDeepCopiesData locks the anti-aliasing guarantee:
// after seeding, mutating the caller's original Data bytes must NOT change the
// bytes carried on the seeded user turn (the message owns an independent copy).
func TestSeedMessagesWithImagesDeepCopiesData(t *testing.T) {
	original := []byte{0x89, 0x50, 0x4e, 0x47}
	images := []ImageBlock{{MediaType: "image/png", Data: original}}

	messages := SeedMessagesWithImages("sys", "describe", images)
	if len(messages) != 2 || len(messages[1].Images) != 1 {
		t.Fatalf("unexpected seeded shape: %#v", messages)
	}

	// Mutate the caller's original bytes (and the caller's slice element) after
	// seeding. An aliased buffer would leak this mutation into the message.
	original[0] = 0x00
	images[0].Data[1] = 0x00

	got := messages[1].Images[0].Data
	want := []byte{0x89, 0x50, 0x4e, 0x47}
	if !bytes.Equal(got, want) {
		t.Fatalf("seeded image bytes = %v, want unchanged %v (data is aliased)", got, want)
	}
}

// TestCloneImageBlocksIsIndependent verifies CloneImageBlocks returns nil for
// nil/empty input and otherwise deep-copies both the slice and each Data buffer.
func TestCloneImageBlocksIsIndependent(t *testing.T) {
	if got := CloneImageBlocks(nil); got != nil {
		t.Fatalf("CloneImageBlocks(nil) = %#v, want nil", got)
	}
	if got := CloneImageBlocks([]ImageBlock{}); got != nil {
		t.Fatalf("CloneImageBlocks(empty) = %#v, want nil", got)
	}

	in := []ImageBlock{{MediaType: "image/png", Data: []byte{1, 2, 3}}}
	out := CloneImageBlocks(in)
	if len(out) != 1 || out[0].MediaType != "image/png" || !bytes.Equal(out[0].Data, []byte{1, 2, 3}) {
		t.Fatalf("CloneImageBlocks copy = %#v", out)
	}
	// Mutating the input must not touch the clone.
	in[0].Data[0] = 9
	if out[0].Data[0] != 1 {
		t.Fatalf("clone Data aliases input: got %d, want 1", out[0].Data[0])
	}
	// And the clone's backing array is distinct from the input's.
	if &in[0].Data[0] == &out[0].Data[0] {
		t.Fatal("clone Data shares backing array with input")
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
