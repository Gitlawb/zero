// Package dictation implements speech-to-text capture and transcription for
// the composer: press a key, talk, and the transcript lands in the input for
// review — never auto-fired at the agent.
//
// Two pipelines share a keybinding and a composer-insertion target:
//
//   - Batch: record to a temp WAV file (Recorder.Start/Stop), then transcribe
//     in one shot (Transcriber.Transcribe) — a local sherpa-onnx-offline exec
//     or a single HTTPS POST to Groq/OpenAI. The only pipeline on Termux.
//   - Streaming: capture raw PCM from the mic tool's stdout
//     (Recorder.StartStreaming), feed 50ms chunks to a websocket
//     (Transcriber.StreamTranscribe), and render partial transcripts live.
//
// Every OS integration shells out via exec.Command with discrete argv — never
// a shell string, never CGO — matching internal/imageinput/clipboard.go. The
// external binaries (arecord/sox/ffmpeg/termux-microphone-record, sherpa-onnx)
// are looked up on PATH and degrade to a clear setup message when missing,
// matching internal/lsp's philosophy for language servers.
package dictation

import (
	"bytes"
	"fmt"
)

// Batch transcription providers (config value stt.provider).
const (
	ProviderLocal  = "local"
	ProviderGroq   = "groq"
	ProviderOpenAI = "openai"
)

// Streaming transcription providers (config value stt.streamProvider).
const (
	StreamProviderLocal    = "local"
	StreamProviderDeepgram = "deepgram"
	StreamProviderOpenAI   = "openai"
)

// BatchProviders lists valid stt.provider values, for config validation.
func BatchProviders() []string {
	return []string{ProviderLocal, ProviderGroq, ProviderOpenAI}
}

// StreamProviders lists valid stt.streamProvider values, for config validation.
func StreamProviders() []string {
	return []string{StreamProviderLocal, StreamProviderDeepgram, StreamProviderOpenAI}
}

// AudioFormat identifies a recorded container format, sniffed from the bytes —
// desktop recorders produce WAV, Termux's recorder produces M4A/AAC.
type AudioFormat string

const (
	FormatWAV     AudioFormat = "wav"
	FormatM4A     AudioFormat = "m4a"
	FormatUnknown AudioFormat = ""
)

// SniffFormat detects the audio container from magic bytes. WAV is
// "RIFF....WAVE"; MP4-family (Termux's .m4a) has "ftyp" at offset 4.
func SniffFormat(data []byte) AudioFormat {
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")) {
		return FormatWAV
	}
	if len(data) >= 8 && bytes.Equal(data[4:8], []byte("ftyp")) {
		return FormatM4A
	}
	return FormatUnknown
}

// FileName returns a transcription-upload filename for the format.
func (f AudioFormat) FileName() string {
	switch f {
	case FormatM4A:
		return "audio.m4a"
	default:
		return "audio.wav"
	}
}

// SetupError reports a missing external tool with install guidance. Missing
// binaries are an expected condition (the user hasn't set dictation up yet),
// not a bug — callers show the hint instead of a raw exec error.
type SetupError struct {
	Tool string
	Hint string
}

func (e *SetupError) Error() string {
	return fmt.Sprintf("%s not found — %s", e.Tool, e.Hint)
}

// AuthError reports that a cloud provider rejected a transcription request for
// authentication reasons (HTTP 401/403) — a missing or invalid API key. It is a
// fixable credential problem, so callers (the TUI) can offer an inline key
// prompt for Provider instead of a dead-end error. Message is the already-
// classified, secret-redacted user-facing text.
type AuthError struct {
	Provider string
	Message  string
}

func (e *AuthError) Error() string { return e.Message }
