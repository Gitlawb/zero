```
 ███████████ ██████████ ███████████      ███████
▒█▒▒▒▒▒▒███ ▒▒███▒▒▒▒▒█▒▒███▒▒▒▒▒███   ███▒▒▒▒▒███
▒     ███▒   ▒███  █ ▒  ▒███    ▒███  ███     ▒▒███
     ███     ▒██████    ▒██████████  ▒███      ▒███
    ███      ▒███▒▒█    ▒███▒▒▒▒▒███ ▒███      ▒███
  ████     █ ▒███ ▒   █ ▒███    ▒███ ▒▒███     ███
 ███████████ ██████████ █████   █████ ▒▒▒███████▒
▒▒▒▒▒▒▒▒▒▒▒ ▒▒▒▒▒▒▒▒▒▒ ▒▒▒▒▒   ▒▒▒▒▒    ▒▒▒▒▒▒▒
```

# Zero

**A clean, terminal-first AI coding agent you fully own — multi-provider, scriptable, and safe by default.**

![runtime](https://img.shields.io/badge/runtime-Bun-14151a?logo=bun&logoColor=fbf0df)
![typescript](https://img.shields.io/badge/TypeScript-strict-3178c6?logo=typescript&logoColor=white)
![tui](https://img.shields.io/badge/TUI-Ink-22d3ee)
![status](https://img.shields.io/badge/status-active%20development-67e8f9)

Zero is a coding agent that lives in your terminal. It runs an agentic tool loop —
reading, editing, searching, and running commands in your repo — against **whatever
model you choose**. One TypeScript codebase powers an interactive TUI and a fully
scriptable headless mode, so the same agent works at your prompt or inside CI.

> Zero treats the **model as a swappable, per-task choice** — no single-vendor lock-in —
> and never mutates your system without a permission decision.

---

## Highlights

- 🔌 **Multi-provider** — OpenAI-compatible, Anthropic, and Gemini behind one interface, with a model registry (capabilities, context limits, cost). Bring your own key and endpoint.
- 🖥️ **Premium TUI** — append-style, scrollback-native Ink interface: streaming responses, compact tool-call rows with inline diffs, a slash-command palette, and a live status footer (model · tokens · cost · context).
- 🤖 **Headless & scriptable** — `zero exec` with clean `text` / `json` / `stream-json` I/O and meaningful exit codes for CI and automation.
- 🧰 **Real tools** — read / write / edit files, `apply_patch`, `grep`, `glob`, `bash`, directory listing, and a live plan/todo.
- 🛡️ **Safe by default** — mutating tools are permission-gated; `--skip-permissions-unsafe` is an explicit, clearly-labeled opt-out.
- 💾 **Durable sessions** — local, append-only session event store with full-text `search`.
- 🩺 **Operable** — built-in `doctor`, `config` inspection, secret redaction everywhere, and `update --check`.

## Quick start

> Requires [Bun](https://bun.com) (version pinned in `package.json`).

```bash
bun install --frozen-lockfile
bun run dev          # launch the interactive TUI
```

Point Zero at a model — either set environment variables:

```bash
export OPENAI_API_KEY=sk-...
# optional: any OpenAI-compatible endpoint / model
export OPENAI_BASE_URL=https://api.openai.com/v1
export OPENAI_MODEL=gpt-4.1
```

…or save reusable provider profiles in `~/.config/zero/config.json` and manage them with
`zero providers`. Run `zero doctor` anytime to verify your setup.

## Usage

### Interactive (TUI)

```bash
bun run dev          # or: zero
```

Inside the TUI: type to chat and press **Enter** to send. `/` opens command suggestions
(**Tab** accepts the first). When the prompt is empty, the arrow keys, **PgUp/PgDn**, and
**Home/End** scroll the transcript. **Ctrl+C** exits.

### Headless (`exec`)

```bash
# one-shot
zero exec "explain src/agent/loop.ts and suggest one improvement"

# from a file, with a specific model, as JSON for scripts
zero exec --file task.md --model claude-sonnet-4.5 --output-format json

# multi-turn / programmatic over stdio
zero exec --input-format stream-json --output-format stream-json < turns.jsonl
```

`exec` flags: `-f, --file` · `-m, --model` · `-C, --cwd` · `-i, --input-format <text|stream-json>` ·
`-o, --output-format <text|json|stream-json>` · `--skip-permissions-unsafe`.
stdout carries **only** program output; logs go to stderr. See
[`docs/STREAM_JSON_PROTOCOL.md`](docs/STREAM_JSON_PROTOCOL.md).

### Other commands

```bash
zero providers list|switch <name>|current   # manage provider profiles
zero search "<query>" [--json --session <id> --type <event>]   # search local sessions
zero doctor [--connectivity] [--json]        # health checks
zero config [--json]                          # inspect resolved configuration
zero update --check [--json]                  # check for a newer release
```

## Providers & models

Selectable per task and per session. The model registry knows each model's provider,
capabilities, context window, and cost.

| Provider | Example models |
|---|---|
| OpenAI-compatible | `gpt-4.1`, `gpt-4.1-mini`, `gpt-4o`, `gpt-4o-mini` |
| Anthropic | `claude-opus-4.1`, `claude-sonnet-4.5`, `claude-haiku-4.5` |
| Google Gemini | `gemini-2.5-pro`, `gemini-2.5-flash`, `gemini-2.5-flash-lite` |

Any OpenAI-compatible endpoint works with just a base URL, key, and model — so local
runtimes (Ollama, gateways, etc.) plug in the same way.

## Tools

| Tool | Purpose | Side effect |
|---|---|---|
| `read_file` · `list_directory` · `grep` · `glob` | explore & search | read |
| `update_plan` | maintain a live task plan | plan |
| `write_file` · `edit_file` · `apply_patch` | create & modify files | write (gated) |
| `bash` | run shell commands | shell (gated) |

Write/shell tools route through the permission policy before any side effect.

## Architecture

```
        TUI (Ink)          headless `exec`           (future) editor ext
            └───────────────────┬───────────────────────┘
                          Agent Core (loop, events, tools)
   ┌──────────┬───────────┬──────────┬──────────┬───────────┬──────────┐
 providers   tools     sessions    usage     redaction    doctor /   stream-json
 + registry  registry  + search   + cost                  config
```

- **Surface-agnostic core**: the agent loop streams text + tool calls, executes tools, and emits a typed event stream consumed identically by every surface.
- **Edges are interfaces**: `Provider`, `Tool`, `SessionStore`, and the permission policy are swappable.
- **Model is data**: capabilities, cost, and routing live in the registry — never hard-coded.

## Project layout

```
src/
  agent/                 # agent loop + system prompts
  cli/                   # headless exec + command surface
  providers/             # openai · anthropic · gemini · base
  tools/                 # read/write/edit/bash/grep/glob/apply_patch/plan
  tui/                   # Ink interface (splash + working view) + startup/
  config/                # layered configuration
  zero-model-registry/   # models, capabilities, cost
  zero-provider-runtime/ # provider resolution/routing
  zero-sessions/         # append-only session event store
  zero-search/           # session search
  zero-usage/            # token usage tracking
  zero-redaction/        # secret redaction
  zero-doctor/           # health checks
  zero-config-inspection/# config inspection
  zero-stream-json/      # headless stream-json protocol
docs/                    # PRD + protocol/install/perf docs
tests/                   # bun test suite
```

## Development

```bash
bun test            # run the test suite
bun run typecheck   # tsc --noEmit
bun run build       # compile a standalone binary
bun run smoke:build # verify the built binary
bun run perf:bench  # performance benchmarks (see docs/PERFORMANCE.md)
```

### Install from a release

```bash
# Linux / macOS
scripts/install.sh

# Windows
powershell -ExecutionPolicy Bypass -File scripts/install.ps1
```

See [`docs/INSTALL.md`](docs/INSTALL.md) for version, repository, and install-path overrides,
and [`docs/UPDATE.md`](docs/UPDATE.md) for the update flow.

## Documentation

- [Product Requirements (PRD)](docs/PRD.md) — vision, goals, full feature spec, roadmap
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md) — headless I/O contract
- [Headless exec PRD](docs/M1_HEADLESS_EXEC_PRD.md)
- [Performance](docs/PERFORMANCE.md) · [Install](docs/INSTALL.md) · [Update](docs/UPDATE.md)

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Please run
`bun test` and `bun run typecheck` before opening a PR.

## License

License is being finalized; a `LICENSE` file will be added before a public release.

---

<sub>Built with Bun · TypeScript · Ink. Zero is the from-scratch successor to <em>openclaude</em>.</sub>
