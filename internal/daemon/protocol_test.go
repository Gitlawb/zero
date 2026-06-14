package daemon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payloads := [][]byte{[]byte("hello"), {}, []byte(strings.Repeat("x", 4096))}
	for _, p := range payloads {
		if err := WriteFrame(&buf, KindCtrl, p); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}
	for i, want := range payloads {
		kind, got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		if kind != KindCtrl {
			t.Fatalf("frame[%d] kind = %d, want KindCtrl", i, kind)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame[%d] payload = %q, want %q", i, got, want)
		}
	}
}

func TestControlRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	msg := Ctrl{Type: CtrlRun, Session: "s1", Cwd: "/tmp", Args: []string{"--prompt", "hi"}}
	if err := WriteControl(&buf, msg); err != nil {
		t.Fatalf("WriteControl: %v", err)
	}
	got, err := ReadControl(&buf)
	if err != nil {
		t.Fatalf("ReadControl: %v", err)
	}
	if got.Type != CtrlRun || got.Session != "s1" || got.Cwd != "/tmp" || len(got.Args) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestWriteFrameRejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFrame(&buf, KindCtrl, make([]byte, MaxFrameSize+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WriteFrame oversize err = %v, want ErrFrameTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("oversize WriteFrame must not emit any bytes, wrote %d", buf.Len())
	}
}

func TestReadFrameRejectsOversizeBeforeAlloc(t *testing.T) {
	// A header advertising a 4 GiB payload must be rejected without reading or
	// allocating that payload.
	header := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(header[:4], MaxFrameSize+1)
	header[4] = byte(KindCtrl)
	_, _, err := ReadFrame(bytes.NewReader(header))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ReadFrame oversize err = %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameUnknownKind(t *testing.T) {
	var buf bytes.Buffer
	// Hand-write a frame with an unknown kind byte (7) and a small payload.
	header := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(header[:4], 2)
	header[4] = 7
	buf.Write(header)
	buf.Write([]byte("ab"))
	_, _, err := ReadFrame(&buf)
	if !errors.Is(err, ErrUnknownFrameKind) {
		t.Fatalf("unknown kind err = %v, want ErrUnknownFrameKind", err)
	}
}

func TestReadFrameTruncated(t *testing.T) {
	// Header promises 10 bytes but only 3 follow → unexpected EOF.
	var buf bytes.Buffer
	header := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(header[:4], 10)
	header[4] = byte(KindCtrl)
	buf.Write(header)
	buf.Write([]byte("abc"))
	_, _, err := ReadFrame(&buf)
	if err == nil || errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("truncated frame err = %v, want a read error", err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestNegotiateVersion(t *testing.T) {
	cases := []struct {
		client int
		want   int
		wantOK bool
	}{
		{client: 1, want: 1, wantOK: true},
		{client: 99, want: ProtoVersion, wantOK: true}, // clamp down to ours
		{client: 0, want: 0, wantOK: false},
		{client: -3, want: 0, wantOK: false},
	}
	for _, tc := range cases {
		got, ok := NegotiateVersion(tc.client)
		if got != tc.want || ok != tc.wantOK {
			t.Fatalf("NegotiateVersion(%d) = (%d,%v), want (%d,%v)", tc.client, got, ok, tc.want, tc.wantOK)
		}
	}
}
