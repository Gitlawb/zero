// Package attest :: attest_test.go
// Transcript chaining, tamper detection, and clawd-zk nullifier
// compatibility vectors.
package attest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTranscriptVerifyRoundTrip(t *testing.T) {
	tr := NewTranscript()
	_ = tr.Append("task_start", 0, map[string]any{"prompt": "hi"})
	_ = tr.Append("llm_turn", 0, map[string]any{"content": "hello"})
	_ = tr.Append("run_done", 0, map[string]any{"answer": "hello"})

	var buf bytes.Buffer
	if err := tr.WriteJSONL(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := VerifyJSONL(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got != tr.CommitmentHex() {
		t.Fatalf("verify %s != commitment %s", got, tr.CommitmentHex())
	}

	tampered := bytes.Replace(buf.Bytes(), []byte("hello"), []byte("hacked"), 1)
	if _, err := VerifyJSONL(bytes.NewReader(tampered)); err == nil {
		t.Fatal("tampered transcript verified")
	}
}

func TestTranscriptDeterminism(t *testing.T) {
	a, b := NewTranscript(), NewTranscript()
	for _, tr := range []*Transcript{a, b} {
		_ = tr.Append("k", 1, map[string]any{"x": 1})
	}
	if a.CommitmentHex() != b.CommitmentHex() {
		t.Fatal("same events, different commitments")
	}
	_ = b.Append("k", 1, map[string]any{"x": 2})
	if a.CommitmentHex() == b.CommitmentHex() {
		t.Fatal("different events, same commitment")
	}
}

func TestNullifierMatchesZkClient(t *testing.T) {
	secret := bytes.Repeat([]byte{0xAB}, 32)
	contextTag := "solana-clawd/attestation/v1"

	// Reference construction, independent of the implementation:
	// SHA-256(secret || utf8(context)) — no nonce.
	h := sha256.New()
	h.Write(secret)
	h.Write([]byte(contextTag))
	want := hex.EncodeToString(h.Sum(nil))

	got, err := Nullifier(secret, contextTag)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got[:]) != want {
		t.Fatalf("nullifier mismatch: %x != %s", got, want)
	}

	// With nonce: SHA-256(secret || context || u64le(7)).
	h = sha256.New()
	h.Write(secret)
	h.Write([]byte(contextTag))
	h.Write([]byte{7, 0, 0, 0, 0, 0, 0, 0})
	want = hex.EncodeToString(h.Sum(nil))
	nonce := uint64(7)
	got, err = NullifierWithNonce(secret, contextTag, &nonce)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got[:]) != want {
		t.Fatal("nonce nullifier mismatch")
	}

	if _, err := Nullifier([]byte("short"), contextTag); err == nil {
		t.Fatal("accepted <16-byte secret")
	}
}

func TestAttestation(t *testing.T) {
	tr := NewTranscript()
	_ = tr.Append("run_done", 0, map[string]any{"answer": "42"})
	secret := bytes.Repeat([]byte{1}, 32)

	att, err := tr.Attest(secret, "zero/run/v1", "test/model")
	if err != nil {
		t.Fatal(err)
	}
	if att.PayloadCommitment != tr.CommitmentHex() {
		t.Fatal("attestation commitment mismatch")
	}
	wantModel := sha256.Sum256([]byte("test/model"))
	if att.ModelHash != "0x"+hex.EncodeToString(wantModel[:]) {
		t.Fatal("modelHash mismatch")
	}
	if att.Schema != attestationSchema || att.Events != 1 {
		t.Fatalf("bad attestation: %+v", att)
	}
}

func TestModelSetID(t *testing.T) {
	a := ModelSetID([]string{"b/model", "a/model", "b/model", ""})
	b := ModelSetID([]string{"a/model", "b/model"})
	if a != b || a != "a/model,b/model" {
		t.Fatalf("ModelSetID not canonical: %q vs %q", a, b)
	}
}
