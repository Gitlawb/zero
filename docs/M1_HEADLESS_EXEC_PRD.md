# M1 Headless Exec PRD

Status: Draft
Owner: Vasanth
Milestone: M1
Scope: `zero exec` CLI surface

## Goal

Ship the first production-shaped headless command for Zero:

```sh
zero exec "inspect this repo and summarize the next fix"
```

This command should be useful for local scripts, CI smoke runs, and later VS Code integration without pulling in the full future protocol, session, MCP, sandbox, or plugin roadmap.

## Non-Goals

- No daemon mode.
- No JSON-RPC input protocol.
- No MCP, plugin, marketplace, or subagent support.
- No session resume/fork/search.
- No new provider adapters in this slice.
- No architecture rewrite of the agent loop.

## User Stories

1. As a CLI user, I can run one prompt headlessly and receive the assistant answer on stdout.
2. As a script author, I can choose `text` or JSONL output.
3. As a reviewer, I can see tool activity without corrupting text stdout.
4. As a model tester, I can override the configured model for one run.
5. As a cautious user, prompt-gated tools stay disabled unless I pass an explicit unsafe flag.

## CLI Contract

```sh
zero exec [prompt...] [options]
```

Options for M1:

- `-f, --file <path>`: read the prompt from a file.
- `-m, --model <model>`: override the configured model for this run.
- `-C, --cwd <path>`: run from another working directory.
- `-o, --output-format <text|json>`: choose stdout format.
- `--skip-permissions-unsafe`: grant prompt-gated tools for this run.

The existing `zero -p "prompt"` shortcut remains as a compatibility path and should call the same runner.

## Output Contract

Text mode:

- stdout contains assistant text only.
- stderr contains tool rows, warnings, and errors.

JSON mode:

- stdout is newline-delimited JSON events.
- Required event types for this slice: `run_start`, `text`, `tool_call`, `tool_result`, `final`, `warning`, `error`, `done`.

## Exit Codes

- `0`: success
- `1`: unexpected crash
- `2`: usage error
- `3`: provider/config/runtime error
- `4`: reserved for future tool failure policy
- `5`: reserved for future permission-denied policy

## Permission Behavior

Default headless mode advertises only tools marked `permission: "allow"`.

`--skip-permissions-unsafe` advertises tools that are not marked `deny` and grants prompt-gated tools through `ToolRegistry.run`. It must not bypass schema validation or direct tool execution safeguards.

## Acceptance Criteria

- `go test ./...` passes.
- `go run ./cmd/zero-release build` passes.
- `go run ./cmd/zero-release smoke` passes.
- `zero exec --help` documents the M1 flags.
- Usage errors do not initialize providers.
- Provider failures return exit code `3`.
- Normal mode does not advertise `bash`, `write_file`, `edit_file`, or `apply_patch`.
- Unsafe mode still uses the registry execution path.
