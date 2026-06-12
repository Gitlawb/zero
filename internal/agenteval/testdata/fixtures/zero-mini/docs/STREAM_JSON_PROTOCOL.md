# Stream JSON Protocol

The fixture documents stream events for local verification commands.

`zero verify --json` emits newline-delimited JSON events. Each event has a
`type` field and a stable task-local payload.

Known event types:

- `check`: one verification check completed.
- `summary`: all verification checks completed.
