// Package attest gives Zero zero-knowledge run attestation: every agent
// run folds its events into a SHA-256 hash chain whose head is a 32-byte
// payload commitment, plus a nullifier proving the run happened exactly
// once. Layouts are bit-compatible with clawd-zk (@clawd/zk-client), so
// commitments publish on-chain via publish_attestation without revealing
// prompts, tool calls, or outputs.
//
// Ported from clawdbot-go pkg/zero (the ClawdBot Zero Engine).
package attest

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	transcriptDomain  = "clawd-zero/transcript/v1"
	attestationSchema = "clawd-zero/attestation/v1"

	// minNullifierSecret mirrors the @clawd/zk-client check.
	minNullifierSecret = 16
)

// ── Record ───────────────────────────────────────────────────────────

// Record is one transcript entry. Field order is fixed by the struct,
// so json.Marshal is canonical for chaining purposes.
type Record struct {
	Index   int             `json:"i"`
	Kind    string          `json:"kind"`
	TaskID  int             `json:"task"`
	Payload json.RawMessage `json:"payload"`
}

// ── Transcript ───────────────────────────────────────────────────────

// Transcript is an append-only hash-chained event log:
//
//	head_0 = SHA-256(transcriptDomain)
//	head_i = SHA-256(head_{i-1} || canonicalJSON(record_i))
type Transcript struct {
	head    [32]byte
	records []Record
}

func NewTranscript() *Transcript {
	return &Transcript{head: sha256.Sum256([]byte(transcriptDomain))}
}

// Append adds a record and folds it into the hash chain.
func (t *Transcript) Append(kind string, taskID int, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("transcript payload: %w", err)
	}
	rec := Record{Index: len(t.records), Kind: kind, TaskID: taskID, Payload: raw}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("transcript record: %w", err)
	}
	h := sha256.New()
	h.Write(t.head[:])
	h.Write(line)
	copy(t.head[:], h.Sum(nil))
	t.records = append(t.records, rec)
	return nil
}

// Commitment returns the current chain head — the payloadCommitment.
func (t *Transcript) Commitment() [32]byte { return t.head }

// CommitmentHex returns the chain head as 0x-prefixed hex.
func (t *Transcript) CommitmentHex() string {
	return "0x" + hex.EncodeToString(t.head[:])
}

func (t *Transcript) Len() int { return len(t.records) }

// WriteJSONL streams the transcript: one record per line, then a final
// commitment line. The file re-verifies with VerifyJSONL.
func (t *Transcript) WriteJSONL(w io.Writer) error {
	bw := bufio.NewWriter(w)
	for _, rec := range t.records {
		line, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if _, err := bw.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	final, _ := json.Marshal(map[string]string{"commitment": t.CommitmentHex()})
	if _, err := bw.Write(append(final, '\n')); err != nil {
		return err
	}
	return bw.Flush()
}

// SaveJSONL writes the transcript to path (0600 — transcripts are private).
func (t *Transcript) SaveJSONL(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.WriteJSONL(f)
}

// VerifyJSONL replays a transcript file and checks the recorded
// commitment against a freshly recomputed chain head.
func VerifyJSONL(r io.Reader) (string, error) {
	head := sha256.Sum256([]byte(transcriptDomain))
	var claimed string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var final struct {
			Commitment string `json:"commitment"`
		}
		if err := json.Unmarshal(line, &final); err == nil && final.Commitment != "" {
			claimed = final.Commitment
			continue
		}
		h := sha256.New()
		h.Write(head[:])
		h.Write(line)
		copy(head[:], h.Sum(nil))
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	computed := "0x" + hex.EncodeToString(head[:])
	if claimed == "" {
		return computed, fmt.Errorf("no commitment line found")
	}
	if claimed != computed {
		return computed, fmt.Errorf("commitment mismatch: file claims %s, replay gives %s", claimed, computed)
	}
	return computed, nil
}

// ── Nullifier — bit-compatible with @clawd/zk-client ─────────────────

// Nullifier computes SHA-256(secret || context), matching
// computeNullifier({secret, context}) with no nonce in clawd-zk.
func Nullifier(secret []byte, context string) ([32]byte, error) {
	return NullifierWithNonce(secret, context, nil)
}

// NullifierWithNonce computes SHA-256(secret || context || nonce_u64le),
// matching computeNullifier({secret, context, nonce}) in clawd-zk.
func NullifierWithNonce(secret []byte, context string, nonce *uint64) ([32]byte, error) {
	var out [32]byte
	if len(secret) < minNullifierSecret {
		return out, fmt.Errorf("nullifier secret must be at least %d bytes", minNullifierSecret)
	}
	h := sha256.New()
	h.Write(secret)
	h.Write([]byte(context))
	if nonce != nil {
		var nb [8]byte
		binary.LittleEndian.PutUint64(nb[:], *nonce)
		h.Write(nb[:])
	}
	copy(out[:], h.Sum(nil))
	return out, nil
}

// ── Attestation ──────────────────────────────────────────────────────

// Attestation is the public artifact of a run: everything needed to call
// clawd-zk publish_attestation (plus a Groth16 proof from the circuit).
// It reveals nothing about prompts, tool calls, or outputs.
type Attestation struct {
	Schema            string `json:"schema"`
	Context           string `json:"context"`
	ModelHash         string `json:"modelHash"`
	PayloadCommitment string `json:"payloadCommitment"`
	Nullifier         string `json:"nullifier"`
	Events            int    `json:"events"`
	CreatedAt         string `json:"createdAt"`
}

// ModelSetID canonicalizes a set of model IDs (dedupe, sort, join) so
// the same model set always hashes to the same modelHash regardless of
// which model won which turn.
func ModelSetID(models []string) string {
	seen := make(map[string]bool, len(models))
	uniq := make([]string, 0, len(models))
	for _, m := range models {
		if m != "" && !seen[m] {
			seen[m] = true
			uniq = append(uniq, m)
		}
	}
	sort.Strings(uniq)
	return strings.Join(uniq, ",")
}

// Attest builds the attestation for a finished transcript.
// modelID is hashed (SHA-256) so the chain learns *which* model class ran
// without the transcript; secret must be >=16 bytes of private material.
func (t *Transcript) Attest(secret []byte, context, modelID string) (*Attestation, error) {
	null, err := Nullifier(secret, context)
	if err != nil {
		return nil, err
	}
	modelHash := sha256.Sum256([]byte(modelID))
	return &Attestation{
		Schema:            attestationSchema,
		Context:           context,
		ModelHash:         "0x" + hex.EncodeToString(modelHash[:]),
		PayloadCommitment: t.CommitmentHex(),
		Nullifier:         "0x" + hex.EncodeToString(null[:]),
		Events:            len(t.records),
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
	}, nil
}
