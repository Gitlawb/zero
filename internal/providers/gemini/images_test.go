package gemini

import (
	"encoding/json"
	"testing"
)

func TestGeminiPartTextOnlySerializationOmitsInlineData(t *testing.T) {
	part := geminiPart{Text: "hello"}
	got, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if string(got) != `{"text":"hello"}` {
		t.Fatalf("text-only part = %s, want byte-identical {\"text\":\"hello\"}", got)
	}
}

func TestGeminiInlineDataSerialization(t *testing.T) {
	part := geminiPart{InlineData: &geminiInlineData{MimeType: "image/png", Data: "QUJD"}}
	got, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if string(got) != `{"inlineData":{"mimeType":"image/png","data":"QUJD"}}` {
		t.Fatalf("inlineData part = %s, want mimeType+data", got)
	}
}
