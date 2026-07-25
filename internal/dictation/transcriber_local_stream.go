package dictation

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/coder/websocket"
)

// localStreamingTranscriber streams audio to the warm sherpa-onnx websocket
// server (§6a). The server keeps encoder state across chunks (a true streaming
// transducer), so partials arrive as decoding proceeds — no redundant
// re-inference.
//
// Wire protocol (verified against sherpa-onnx's reference client, NOT the
// plan's "16-bit PCM" description, which was imprecise): the client sends
// binary frames of little-endian float32 samples normalized to [-1, 1]
// (int16/32768), then the text "Done" to end; the server replies with JSON
// result messages and finally the text "Done!" to signal completion.
type localStreamingTranscriber struct {
	manager *ServerManager
}

// NewLocalStreamingTranscriber builds a streaming transcriber backed by the
// shared, session-long sherpa-onnx server manager.
func NewLocalStreamingTranscriber(manager *ServerManager) Transcriber {
	return &localStreamingTranscriber{manager: manager}
}

// Transcribe is not this transcriber's job — the streaming path uses
// StreamTranscribe; the batch/Termux fallback uses the offline transcriber.
func (l *localStreamingTranscriber) Transcribe(context.Context, []byte) (string, error) {
	return "", errors.New("local streaming transcriber does not support batch transcription")
}

func (l *localStreamingTranscriber) StreamTranscribe(ctx context.Context, chunks <-chan []byte, onPartial func(string, bool)) (string, error) {
	url, err := l.manager.EnsureRunning(ctx)
	if err != nil {
		return "", err
	}
	conn, err := dialWithOneRestart(ctx, l.manager, url)
	if err != nil {
		return "", fmt.Errorf("connecting to sherpa-onnx server: %w", err)
	}
	defer conn.CloseNow()

	// Writer goroutine: forward converted chunks, then signal end. A write error
	// (server crashed) cancels the read side via writeErr.
	writeErrCh := make(chan error, 1)
	go func() {
		for chunk := range chunks {
			if err := conn.Write(ctx, websocket.MessageBinary, pcm16ToFloat32LE(chunk)); err != nil {
				writeErrCh <- err
				return
			}
		}
		writeErrCh <- conn.Write(ctx, websocket.MessageText, []byte("Done"))
	}()

	// The server reports "text" PER SEGMENT, not cumulatively for the whole
	// utterance: when it detects an endpoint (a pause between phrases, or the
	// trailing silence when recording stops) it finalizes the current segment, bumps
	// "segment", and resets "text" to "" for the next one. So we accumulate the text
	// of every segment — otherwise a new segment's (often empty) result would
	// overwrite everything already transcribed, and the transcript would vanish on
	// stop. Displayed/returned text is all segments joined in order.
	var segmentTexts []string
	joinSegments := func() string {
		parts := make([]string, 0, len(segmentTexts))
		for _, s := range segmentTexts {
			if t := strings.TrimSpace(s); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, " ")
	}
	lastText := ""
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			// Mid-stream failure: return the best-effort text accumulated so far
			// plus the error — already-streamed partials are never discarded (§6).
			select {
			case werr := <-writeErrCh:
				if werr != nil {
					err = werr
				}
			default:
			}
			return lastText, fmt.Errorf("sherpa-onnx stream error: %w", err)
		}
		if typ == websocket.MessageText && strings.TrimSpace(string(data)) == "Done!" {
			break
		}
		text, segment, final, ok := parseSherpaStreamResult(data)
		if !ok {
			continue
		}
		if segment < 0 {
			segment = 0
		}
		for len(segmentTexts) <= segment {
			segmentTexts = append(segmentTexts, "")
		}
		segmentTexts[segment] = text
		lastText = joinSegments()
		if onPartial != nil {
			onPartial(lastText, final)
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	return lastText, nil
}

// dialWithOneRestart dials the server, and on failure restarts it once and
// retries — mirroring the LSP manager's single crashed-server recovery (§6/§6a).
func dialWithOneRestart(ctx context.Context, manager *ServerManager, url string) (*websocket.Conn, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		return conn, nil
	}
	// The warm server may have died between EnsureRunning and Dial; reap and
	// restart once.
	_ = manager.Shutdown(ctx)
	url, rerr := manager.EnsureRunning(ctx)
	if rerr != nil {
		return nil, rerr
	}
	conn, _, err = websocket.Dial(ctx, url, nil)
	return conn, err
}

// parseSherpaStreamResult extracts one server JSON result. "text" is the
// hypothesis for the CURRENT segment (segment index in "segment"), which resets
// each time the recognizer endpoints — so the caller must accumulate per segment,
// not treat "text" as the whole-utterance transcript. "is_final" marks a settled
// segment.
func parseSherpaStreamResult(data []byte) (text string, segment int, final bool, ok bool) {
	var parsed struct {
		Text    string `json:"text"`
		Segment int    `json:"segment"`
		IsFinal bool   `json:"is_final"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", 0, false, false
	}
	return strings.TrimSpace(parsed.Text), parsed.Segment, parsed.IsFinal, true
}

// pcm16ToFloat32LE converts a little-endian int16 PCM chunk to the little-endian
// float32 samples (normalized to [-1, 1]) the sherpa-onnx server expects.
func pcm16ToFloat32LE(pcm []byte) []byte {
	n := len(pcm) / 2
	out := make([]byte, n*4)
	for i := 0; i < n; i++ {
		s := int16(uint16(pcm[2*i]) | uint16(pcm[2*i+1])<<8)
		bits := math.Float32bits(float32(s) / 32768.0)
		binary.LittleEndian.PutUint32(out[4*i:], bits)
	}
	return out
}
