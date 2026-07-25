package dictation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsFiltersAndFlags(t *testing.T) {
	assets := []map[string]any{
		{"name": "sherpa-onnx-streaming-zipformer-en-2023-06-26.tar.bz2", "size": 1 << 20, "digest": "sha256:abc"},
		{"name": "sherpa-onnx-whisper-tiny.en.tar.bz2", "size": 2 << 20},
		{"name": "sherpa-onnx-moonshine-tiny-en-int8.tar.bz2", "size": 3 << 20},                  // curated but not flagged recommended
		{"name": "sherpa-onnx-streaming-zipformer-en-kroko-2025-08-06.tar.bz2", "size": 1 << 20}, // curated → recommended
		{"name": "sherpa-onnx-paraformer-zh-2024-03-09.tar.bz2", "size": 4 << 20},
		// Non-ASR tools that MUST be filtered out:
		{"name": "sherpa-onnx-vad-silero.tar.bz2", "size": 1},
		{"name": "vits-piper-en_US-amy.tar.bz2", "size": 1},
		{"name": "sherpa-onnx-punct-en.tar.bz2", "size": 1},
		{"name": "sherpa-onnx-kws-zipformer.tar.bz2", "size": 1},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "assets": assets})
	}))
	defer srv.Close()

	vs, err := ListModels(context.Background(), http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 5 {
		t.Fatalf("expected 5 transcription models (tools filtered), got %d", len(vs))
	}
	byID := map[string]ModelVariant{}
	for _, v := range vs {
		byID[v.ID] = v
	}
	if !byID["streaming-zipformer-en-2023-06-26"].Streaming {
		t.Error("streaming model should be flagged Streaming")
	}
	if byID["whisper-tiny.en"].Streaming {
		t.Error("whisper is batch, not streaming")
	}
	if !byID["streaming-zipformer-en-kroko-2025-08-06"].Recommended {
		t.Error("a curated model flagged Recommended should be Recommended")
	}
	if byID["moonshine-tiny-en-int8"].Recommended {
		t.Error("moonshine tiny is curated but no longer flagged Recommended (no star)")
	}
	if byID["paraformer-zh-2024-03-09"].Recommended {
		t.Error("an uncurated model should not be Recommended")
	}
	// A curated model with no API digest still gets its pinned digest.
	if byID["moonshine-tiny-en-int8"].Digest == "" {
		t.Error("curated model should carry a pinned digest")
	}
	// An uncurated model with no API digest has an empty digest (TLS-only download).
	if byID["whisper-tiny.en"].Digest != "" {
		t.Error("uncurated model without an API digest should have an empty digest")
	}
	// The API-provided digest is used when present.
	if byID["streaming-zipformer-en-2023-06-26"].Digest != "abc" {
		t.Errorf("expected the API digest, got %q", byID["streaming-zipformer-en-2023-06-26"].Digest)
	}
}
