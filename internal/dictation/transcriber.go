package dictation

import "context"

// Transcriber turns captured audio into text. Batch (Transcribe) and streaming
// (StreamTranscribe) are one interface but two independent code paths — a
// batch-only provider (Groq/OpenAI, sherpa-onnx-offline) leaves
// StreamTranscribe returning ErrStreamingUnsupported, and a streaming provider
// still implements Transcribe for the batch/Termux fallback.
type Transcriber interface {
	// Transcribe returns the final text for one recorded clip.
	Transcribe(ctx context.Context, audio []byte) (string, error)

	// StreamTranscribe consumes PCM16 chunks and reports incremental results
	// via onPartial(text, final) as decoding proceeds, returning the complete
	// final transcript when the chunk channel closes.
	//
	// onPartial's text is the CURRENT best transcript of the utterance so far
	// (cumulative, not a delta) so the composer can replace the live region
	// wholesale; final marks a segment the provider considers settled.
	//
	// On a mid-stream failure (network drop for cloud, subprocess crash for
	// local) StreamTranscribe returns the best-effort text accumulated from
	// onPartial calls made so far PLUS a non-nil error — already-streamed text
	// is never discarded, only the untranscribed tail after the failure is lost.
	StreamTranscribe(ctx context.Context, chunks <-chan []byte, onPartial func(text string, final bool)) (string, error)
}

// SampleRate lets a Transcriber declare the capture rate its wire format
// expects, so the recorder can be built to match (OpenAI Realtime wants 24kHz;
// everything else uses 16kHz). A Transcriber that does not implement this uses
// DefaultSampleRate.
type SampleRate interface {
	SampleRate() int
}

// RequiredSampleRate returns the capture rate a Transcriber needs.
func RequiredSampleRate(t Transcriber) int {
	if sr, ok := t.(SampleRate); ok {
		if rate := sr.SampleRate(); rate > 0 {
			return rate
		}
	}
	return DefaultSampleRate
}
