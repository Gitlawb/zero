# Zero Stream-JSON Protocol

Zero stream-json is a line-delimited protocol for headless clients such as
editor extensions and automation wrappers.

Every line is one JSON object. Empty input lines are ignored. Output events are
redacted before they are written to stdout.

## Version

Current schema version: `1`

Every input and output event must include:

```json
{ "schemaVersion": 1, "type": "..." }
```

Output events also include `runId`.

## Input Events

`zero exec --input-format stream-json` accepts these JSONL events from stdin or
`--file`.

```json
{ "schemaVersion": 1, "type": "message", "role": "user", "content": "Inspect this repo." }
{ "schemaVersion": 1, "type": "prompt", "content": "Return only blockers." }
```

Zero combines accepted input event content in order, separated by blank lines.
Unknown fields are rejected so protocol clients catch drift early.

## Output Events

`zero exec --output-format stream-json` emits schema-versioned JSONL events.

```json
{ "schemaVersion": 1, "type": "run_start", "runId": "run_20260603_abc123", "sessionId": "zero_20260603100000_abc123", "cwd": "/repo", "provider": "openai", "model": "gpt-4.1", "apiModel": "gpt-4.1" }
{ "schemaVersion": 1, "type": "text", "runId": "run_20260603_abc123", "delta": "..." }
{ "schemaVersion": 1, "type": "tool_call", "runId": "run_20260603_abc123", "id": "call_1", "name": "read_file", "args": { "path": "README.md" }, "sideEffect": "read" }
{ "schemaVersion": 1, "type": "permission_request", "runId": "run_20260603_abc123", "id": "call_2", "name": "write_file", "action": "prompt", "permission": "prompt", "permissionMode": "ask", "sideEffect": "write", "reason": "Creates or overwrites files." }
{ "schemaVersion": 1, "type": "permission_decision", "runId": "run_20260603_abc123", "id": "call_2", "name": "write_file", "action": "allow", "permission": "prompt", "permissionGranted": true, "decisionReason": "approved in TUI" }
{ "schemaVersion": 1, "type": "tool_result", "runId": "run_20260603_abc123", "id": "call_1", "status": "ok", "output": "...", "truncated": false }
{ "schemaVersion": 1, "type": "usage", "runId": "run_20260603_abc123", "promptTokens": 12, "completionTokens": 8, "totalTokens": 20 }
{ "schemaVersion": 1, "type": "final", "runId": "run_20260603_abc123", "text": "..." }
{ "schemaVersion": 1, "type": "run_end", "runId": "run_20260603_abc123", "status": "success", "exitCode": 0 }
```

Errors are part of the protocol and are followed by `run_end`.

Headless `exec` has no interactive permission responder. If a prompt-gated tool
is not pre-approved, Zero may emit `permission_request` followed by a denied
`tool_result`; interactive surfaces emit `permission_decision` when the user
allows, denies, or always-allows the request.

```json
{ "schemaVersion": 1, "type": "error", "runId": "run_20260603_abc123", "code": "provider_error", "message": "...", "recoverable": false }
{ "schemaVersion": 1, "type": "run_end", "runId": "run_20260603_abc123", "status": "error", "exitCode": 3 }
```
