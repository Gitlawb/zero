package dictation

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudTranscribeBuildsMultipartAndParsesText(t *testing.T) {
	var gotModel, gotAuth, gotFilename string
	var gotFileBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Fatalf("bad content type %q: %v", r.Header.Get("Content-Type"), err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("multipart: %v", err)
			}
			switch part.FormName() {
			case "model":
				b, _ := io.ReadAll(part)
				gotModel = string(b)
			case "file":
				gotFilename = part.FileName()
				gotFileBytes, _ = io.ReadAll(part)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"  hello world  "}`)
	}))
	defer srv.Close()

	tr, err := NewCloudTranscriber(CloudConfig{
		Provider: ProviderGroq,
		BaseURL:  srv.URL,
		APIKey:   "secret-key",
		Model:    "whisper-large-v3-turbo",
	})
	if err != nil {
		t.Fatalf("NewCloudTranscriber: %v", err)
	}
	audio := wavFixture()
	text, err := tr.Transcribe(context.Background(), audio)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text = %q, want trimmed 'hello world'", text)
	}
	if gotModel != "whisper-large-v3-turbo" {
		t.Errorf("model field = %q", gotModel)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotFilename != "audio.wav" {
		t.Errorf("filename = %q, want audio.wav", gotFilename)
	}
	if len(gotFileBytes) != len(audio) {
		t.Errorf("uploaded %d file bytes, want %d", len(gotFileBytes), len(audio))
	}
}

func TestCloudTranscribeM4AFilename(t *testing.T) {
	var gotFilename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "file" {
				gotFilename = part.FileName()
			}
		}
		io.WriteString(w, `{"text":"ok"}`)
	}))
	defer srv.Close()

	tr, _ := NewCloudTranscriber(CloudConfig{Provider: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"})
	if _, err := tr.Transcribe(context.Background(), m4aFixture()); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotFilename != "audio.m4a" {
		t.Errorf("filename = %q, want audio.m4a", gotFilename)
	}
}

func TestCloudTranscribeAuthErrorClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid api key sk-secret-key"}}`)
	}))
	defer srv.Close()

	tr, _ := NewCloudTranscriber(CloudConfig{Provider: ProviderGroq, BaseURL: srv.URL, APIKey: "sk-secret-key", Model: "whisper-large-v3-turbo"})
	_, err := tr.Transcribe(context.Background(), wavFixture())
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if strings.Contains(err.Error(), "sk-secret-key") {
		t.Errorf("API key leaked into error: %v", err)
	}
	if !strings.Contains(err.Error(), "auth error") {
		t.Errorf("expected auth-error classification, got %v", err)
	}
}

func TestNewCloudTranscriberRequiresKey(t *testing.T) {
	_, err := NewCloudTranscriber(CloudConfig{Provider: ProviderGroq, BaseURL: "https://api.groq.com/openai/v1", Model: "m"})
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError for missing key, got %v", err)
	}
}

func TestCloudTranscribeStreamingUnsupported(t *testing.T) {
	tr, _ := NewCloudTranscriber(CloudConfig{Provider: ProviderGroq, BaseURL: "https://x", APIKey: "k", Model: "m"})
	_, err := tr.StreamTranscribe(context.Background(), nil, nil)
	if !errors.Is(err, ErrStreamingUnsupported) {
		t.Errorf("want ErrStreamingUnsupported, got %v", err)
	}
}
