package providermodelcatalog

import "testing"

func TestIsSTTModelID(t *testing.T) {
	in := []string{"whisper-1", "whisper-large-v3-turbo", "distil-whisper-large-v3-en",
		"gpt-4o-transcribe", "gpt-4o-mini-transcribe", "nova-3-scribe", "parakeet-tdt-1.1b", "canary-1b"}
	for _, id := range in {
		if !IsSTTModelID(id) {
			t.Errorf("%q should be an STT model by name", id)
		}
	}
	out := []string{"gpt-4o", "claude-sonnet-4", "tts-1", "gpt-4o-mini-tts",
		"dall-e-3", "text-to-speech-1", ""}
	for _, id := range out {
		if IsSTTModelID(id) {
			t.Errorf("%q should NOT be an STT model by name", id)
		}
	}
}

func TestIsSTTModelUsesModalities(t *testing.T) {
	// Audio in, text out → transcriber, even with an unfamiliar name.
	if !IsSTTModel(Model{ID: "acme-speech-v2", InputModalities: []string{"audio"}, OutputModalities: []string{"text"}}) {
		t.Error("audio-in/text-out model should be classified STT regardless of name")
	}
	// Audio out → TTS or speech-to-speech, never STT, even if the name says "transcribe".
	if IsSTTModel(Model{ID: "weird-transcribe-tts", InputModalities: []string{"text"}, OutputModalities: []string{"audio"}}) {
		t.Error("an audio-OUTPUT model must never be classified STT")
	}
	// Realtime speech-to-speech: audio in AND audio out → not a transcriber.
	if IsSTTModel(Model{ID: "gpt-4o-realtime", InputModalities: []string{"audio", "text"}, OutputModalities: []string{"audio", "text"}}) {
		t.Error("audio-in AND audio-out (speech-to-speech) must not be classified STT")
	}
	// Text-only LLM: input declared, no audio → not STT.
	if IsSTTModel(Model{ID: "gpt-4o", InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}}) {
		t.Error("a text/image-in model must not be classified STT")
	}
	// No modality metadata: fall back to the name heuristic.
	if !IsSTTModel(Model{ID: "whisper-large-v3-turbo"}) {
		t.Error("with no modalities, a whisper id should fall back to STT-true")
	}
	if IsSTTModel(Model{ID: "gpt-4o-mini-tts"}) {
		t.Error("with no modalities, a tts id should be STT-false")
	}
}
