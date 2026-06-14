// Package daemon implements zero's long-running daemon/server mode: a local
// control server that supervises a pool of headless `zero exec` worker processes
// and routes multiple agent sessions to them over an owner-only local socket.
//
// It reuses zero's existing building blocks rather than inventing new ones:
//   - internal/background : child-process group setup + cross-platform terminate.
//   - internal/streamjson : the line-based agent event protocol on worker stdio.
//   - internal/cli (exec) : a worker is a `zero exec -i/-o stream-json` process.
//
// The control plane (this file) is a small framed codec mirrored from the
// reference daemon's protocol.js: a 4-byte big-endian length prefix, a 1-byte
// frame kind, then a JSON control payload, with a 1 MiB frame cap and a
// version-negotiation handshake. The agent event stream itself stays stream-json.
package daemon

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Wire constants. Mirrors reference-daemon-code-agent-js/protocol.js
// (FRAME_DATA/FRAME_CTRL, HEADER_SIZE, MAX_FRAME_SIZE, PROTO_VERSION).
const (
	// ProtoVersion is the control-protocol version advertised in the handshake.
	ProtoVersion = 1
	// MaxFrameSize caps a single control frame payload at 1 MiB. A larger
	// advertised length is rejected before any allocation (fail closed).
	MaxFrameSize = 1 << 20
	// frameHeaderSize is 4 bytes big-endian length + 1 byte kind.
	frameHeaderSize = 5
)

// FrameKind tags a frame's payload. Mirrors protocol.js FRAME_DATA/FRAME_CTRL.
type FrameKind uint8

const (
	// KindData carries an opaque payload (e.g. a stream-json line). Reserved for
	// future use; the control plane uses KindCtrl.
	KindData FrameKind = 0
	// KindCtrl carries a JSON-encoded control message (see Ctrl).
	KindCtrl FrameKind = 1
)

// ErrFrameTooLarge is returned when a frame's advertised payload length exceeds
// MaxFrameSize. The reader rejects it without allocating the buffer.
var ErrFrameTooLarge = errors.New("daemon: control frame exceeds size cap")

// ErrUnknownFrameKind is returned for a frame whose kind byte is not recognized.
var ErrUnknownFrameKind = errors.New("daemon: unknown control frame kind")

// WriteFrame writes a single framed message: [uint32BE len][uint8 kind][payload].
// It rejects an over-cap payload before writing anything so a buggy/hostile caller
// cannot emit a frame a conforming reader would refuse.
func WriteFrame(w io.Writer, kind FrameKind, payload []byte) error {
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("%w (%d > %d)", ErrFrameTooLarge, len(payload), MaxFrameSize)
	}
	header := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)))
	header[4] = byte(kind)
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads a single framed message. It validates the advertised length
// against MaxFrameSize BEFORE allocating, so an oversize or hostile frame is
// rejected (ErrFrameTooLarge) rather than triggering a huge allocation. An
// unrecognized kind byte yields ErrUnknownFrameKind after the payload is drained
// so the stream stays in sync.
func ReadFrame(r io.Reader) (FrameKind, []byte, error) {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[:4])
	if length > MaxFrameSize {
		return 0, nil, fmt.Errorf("%w (%d > %d)", ErrFrameTooLarge, length, MaxFrameSize)
	}
	kind := FrameKind(header[4])
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	if kind != KindData && kind != KindCtrl {
		return kind, nil, ErrUnknownFrameKind
	}
	return kind, payload, nil
}

// CtrlType is the discriminator for a control message.
type CtrlType string

const (
	// CtrlHello is the client's opening handshake; Version is the highest control
	// version it speaks.
	CtrlHello CtrlType = "hello"
	// CtrlHelloOK is the daemon's handshake reply with the negotiated Version.
	CtrlHelloOK CtrlType = "hello_ok"
	// CtrlRun asks the daemon to create/route a session and stream its output.
	CtrlRun CtrlType = "run"
	// CtrlAttach asks the daemon to attach the client to a running session's stream.
	CtrlAttach CtrlType = "attach"
	// CtrlStatus requests daemon/worker/session status.
	CtrlStatus CtrlType = "status"
	// CtrlStatusResult carries the status payload (see StatusReport in Status).
	CtrlStatusResult CtrlType = "status_result"
	// CtrlData carries one stream-json line of agent output back to the client.
	CtrlData CtrlType = "data"
	// CtrlAck acknowledges receipt/dispatch of a request (at-least-once dispatch).
	CtrlAck CtrlType = "ack"
	// CtrlEnd marks the end of a session's stream.
	CtrlEnd CtrlType = "end"
	// CtrlShutdown asks the daemon to drain and stop.
	CtrlShutdown CtrlType = "shutdown"
	// CtrlError carries a human-readable error for the client.
	CtrlError CtrlType = "error"
)

// Ctrl is a control message carried in a KindCtrl frame as JSON. Unused fields
// are omitted so frames stay small.
type Ctrl struct {
	Type    CtrlType `json:"type"`
	Version int      `json:"version,omitempty"`
	Session string   `json:"session,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Args    []string `json:"args,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	// Line is one stream-json event line (for CtrlData).
	Line string `json:"line,omitempty"`
	// Message is a human-readable detail (for CtrlError / CtrlAck).
	Message string `json:"message,omitempty"`
	// Status is populated on CtrlStatusResult.
	Status *StatusReport `json:"status,omitempty"`
}

// WriteControl marshals a control message into a KindCtrl frame.
func WriteControl(w io.Writer, msg Ctrl) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return WriteFrame(w, KindCtrl, payload)
}

// ReadControl reads the next frame and decodes it as a control message. A
// non-control frame yields ErrUnknownFrameKind so the caller fails closed.
func ReadControl(r io.Reader) (Ctrl, error) {
	kind, payload, err := ReadFrame(r)
	if err != nil {
		return Ctrl{}, err
	}
	if kind != KindCtrl {
		return Ctrl{}, ErrUnknownFrameKind
	}
	var msg Ctrl
	if err := json.Unmarshal(payload, &msg); err != nil {
		return Ctrl{}, fmt.Errorf("daemon: decode control message: %w", err)
	}
	return msg, nil
}

// NegotiateVersion picks the protocol version both ends can speak: the lower of
// the client's advertised version and ProtoVersion. A non-positive client
// version is invalid and yields ok=false so the daemon can reject the connection.
func NegotiateVersion(clientVersion int) (int, bool) {
	if clientVersion <= 0 {
		return 0, false
	}
	if clientVersion < ProtoVersion {
		return clientVersion, true
	}
	return ProtoVersion, true
}
