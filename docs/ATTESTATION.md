# Zero-Knowledge Run Attestation

`internal/attest` makes "Zero" literal: agent runs can be committed and
proven without revealing anything about them, and the package itself is
gated against recursion.

## What it provides

| Piece | Meaning |
|---|---|
| `Transcript` | Append-only SHA-256 hash chain over run events. `head_i = SHA-256(head_{i-1} ‖ canonicalJSON(record_i))`. |
| `CommitmentHex()` | The chain head — a 32-byte payload commitment for on-chain attestation. |
| `Nullifier(secret, context)` | `SHA-256(secret ‖ context ‖ nonce?)`, bit-compatible with `@clawd/zk-client` `computeNullifier`. Proves a run happened exactly once. |
| `Attest(...)` | Public artifact carrying the four public inputs of clawd-zk `publish_attestation`: attester, modelHash, payloadCommitment, nullifier. |
| `VerifyJSONL` | Anyone holding the transcript file can replay the chain and check the commitment locally. |
| `norecursion_test.go` | Static call-graph gate: any direct or mutual recursion in the package fails `go test`. Zero means zero recursion. |

## Privacy model

The transcript stays local (or off-chain encrypted). Only the 32-byte
commitment, the nullifier, and a model-set hash are ever published. The
chain learns *that* a run happened, *which* model set produced it, and
that it happened *exactly once* — never prompts, tool calls, or outputs.

## Flow

```go
tr := attest.NewTranscript()
tr.Append("task_start", 0, map[string]any{"prompt": prompt})
// ... one Append per LLM turn / tool call / task completion ...
tr.Append("run_done", 0, map[string]any{"answer": answer})

att, _ := tr.Attest(secret, "zero/run/v1", attest.ModelSetID(models))
// att.PayloadCommitment + att.Nullifier + att.ModelHash → Groth16 circuit
// → clawd-zk publish_attestation (see the clawdbot-go zk-primitives repo)
```

Reference implementation with the full flat-scheduler agent loop, ZK God
Mode model racing, and NL intent routing lives in ClawdBot's `pkg/zero`
(clawdbot-go `docs/ZERO.md`).
