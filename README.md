<div align="center">

```
███████╗ ███████╗ ██████╗   ██████╗
╚══███╔╝ ██╔════╝ ██╔══██╗ ██╔═══██╗
  ███╔╝  █████╗   ██████╔╝ ██║   ██║
 ███╔╝   ██╔══╝   ██╔══██╗ ██║   ██║
███████╗ ███████╗ ██║  ██║ ╚██████╔╝
╚══════╝ ╚══════╝ ╚═╝  ╚═╝  ╚═════╝
```

### The terminal coding agent you fully own.

**Any model. Any provider. Your rules.**

![go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
![providers](https://img.shields.io/badge/providers-25%2B-34E2EA)
![tests](https://img.shields.io/badge/test%20files-200%2B-43D17A)
![status](https://img.shields.io/badge/status-active%20development-E8B84B)

</div>

---

Zero is an AI coding agent that lives in your terminal. It runs a full agentic loop — reading, searching, editing, and executing in your repo — against **whatever model you choose**: frontier APIs, fast cloud inference, or a local model on your own machine. One interface, no vendor lock-in, no telemetry phoning home.

```bash
zero                                          # interactive TUI
zero exec "fix the failing test in ./pkg"     # headless one-shot
zero exec -o stream-json < turns.jsonl        # programmatic, for scripts & CI
```

> Zero treats the **model as a swappable, per-task choice** and **never mutates your system without a permission decision**.

## Why Zero

- 🔌 **25+ providers, one interface** — OpenAI, Anthropic, Gemini, Ollama (local & cloud), LM Studio, OpenRouter, Groq, DeepSeek, Mistral, xAI, Qwen, Kimi, GitHub Models, and any OpenAI- or Anthropic-compatible endpoint. Switch mid-session with `/model`.
- 🖥️ **A TUI that feels premium** — truecolor Bubble Tea interface with a first-run setup wizard, searchable live model picker, scrollback, themes, image input, and slash commands for everything.
- 🤖 **Headless & scriptable** — `zero exec` with `text` / `json` / `stream-json` I/O, session resume & fork, isolated `--worktree` runs, and meaningful exit codes. Built for CI.
- 🧠 **Subagents** — delegate to built-in `worker`, `explorer`, and `code-review` specialists (or generate your own) that run as real background tasks, even out-of-process.
- 📋 **Spec mode** — have the agent draft a spec first, review and approve it, *then* let it build. No more runaway sessions.
- 📈 **Mid-run model escalation** — start cheap, and let the agent request a stronger model only when it hits a wall (`--allow-escalation`).
- 🗺️ **Repo intelligence** — deterministic repo maps, workspace indexing, and context-budget reports keep the agent grounded in *your* codebase, not hallucinations.
- ⏰ **Scheduled agents** — `zero cron` runs file-backed, dependency-free agent jobs on a schedule.
- 🛡️ **Safe by default** — permission-gated mutations, autonomy ceilings, sandbox policy (writes stay inside the workspace unless you grant extra directories with `--add-dir` / `/add-dir`), and secret redaction everywhere. Unsafe mode is an explicit, loudly-labeled opt-in.
- 💾 **Durable sessions** — append-only local event store with full-text search, resume, fork, and rewind. Your history never leaves your disk.
- 🧩 **Extensible** — skills, plugins, hooks, and MCP (Zero is both an MCP client *and* an MCP server).

## Quick start

```bash
# run from source (requires Go 1.24+)
go run ./cmd/zero

# or install a release binary
scripts/install.sh                                          # Linux / macOS
powershell -ExecutionPolicy Bypass -File scripts/install.ps1  # Windows
```

First launch opens a **guided setup wizard** — pick a provider, paste a key, choose a model, done. Or do it non-interactively:

```bash
export OPENAI_API_KEY=sk-...      # or ANTHROPIC_API_KEY, GEMINI_API_KEY, GROQ_API_KEY, ...
zero setup                        # guided first-run provider setup
zero doctor                       # verify config, keys, and connectivity
```

Local models need no key at all:

```bash
# Ollama or LM Studio running locally? Zero finds them.
zero providers list
```

## The TUI

Type to chat, **Enter** to send. `/` opens command suggestions, **Shift+Tab** cycles permission modes, **Ctrl+C** exits.

| | |
|---|---|
| `/model` `/provider` | switch model or provider mid-session (searchable picker) |
| `/spec` `/plan` | spec-mode drafting and live plan view |
| `/image` | attach images for vision models |
| `/resume` `/rewind` | time-travel across sessions |
| `/compact` `/context` | manage the context window |
| `/permissions` `/tools` | inspect what the agent can touch |
| `/add-dir` | grant an extra write directory for the session, or list current write roots |
| `/theme` `/style` | make it yours |
| `/doctor` `/usage` `/config` | health, cost, and config without leaving the chat |

Turn-completion notifications (terminal bell / OSC-9) ping you when the agent finishes or needs input — go make coffee.

## Headless `exec`

```bash
# one-shot
zero exec "explain internal/agent/loop.go and suggest one improvement"

# pick a model and mode preset per task
zero exec --model claude-sonnet-4.5 --mode deep "refactor the session store"

# spec-first: draft → review → approve → build
zero exec --use-spec "add rate limiting to the API client"

# run in an isolated git worktree, escalate model only if needed
zero exec -w --allow-escalation "migrate the config loader to v2"

# multi-turn programmatic I/O over stdio
zero exec --input-format stream-json --output-format stream-json < turns.jsonl

# resume or fork any previous session
zero exec --resume            # latest
zero exec --fork <session-id> "now try the other approach"
```

Key flags: `-m/--model` · `--mode <smart|deep|fast|large|precise>` · `--image` · `--use-spec` · `--auto <low|medium|high>` · `--enabled-tools/--disabled-tools` · `-w/--worktree` · `--add-dir <path>` (repeatable) · `--resume/--fork` · `--allow-escalation` · `--notify` · `-o <text|json|stream-json>`.

stdout carries **only** program output; logs go to stderr. Full contract in [`docs/STREAM_JSON_PROTOCOL.md`](docs/STREAM_JSON_PROTOCOL.md).

## Commands

```
zero                  interactive TUI
zero exec             one-shot / scripted agent runs
zero setup            guided first-run provider setup
zero models           model registry (capabilities, context, cost)
zero providers        provider profiles + 25-provider catalog
zero doctor           config, key, and connectivity health checks
zero context          workspace context-budget report
zero repo-map         deterministic repository map for agent context
zero repo-info        local (network-free) repository characterizer
zero search | find    full-text search over local session history
zero sessions         session lineage inspection
zero spec             review & approve saved spec-mode drafts
zero specialist       manage subagent profiles
zero skills           markdown instruction skills
zero plugins          plugin manifests
zero hooks            lifecycle hook configuration
zero mcp              MCP client settings
zero serve --mcp      expose Zero's tools over MCP stdio
zero sandbox          sandbox policy & persistent grants
zero worktrees        isolated git worktrees for agent runs
zero verify           detect & run local verification checks
zero changes          inspect & commit local git changes
zero usage            token usage and estimated cost
zero cron             scheduled agent jobs (file-backed, dep-free)
zero update           check for newer releases
```

## Providers

Bring your own key — or no key at all for local runtimes.

| Tier | Providers |
|---|---|
| **Frontier APIs** | OpenAI · Anthropic · Google Gemini |
| **Fast cloud inference** | Groq · OpenRouter · Together AI · DeepSeek · Mistral · xAI · NVIDIA NIM |
| **Local — no key, no cloud** | Ollama · LM Studio |
| **More clouds** | Ollama Cloud · DashScope (Qwen) · Moonshot (Kimi) · MiniMax · Z.ai · Venice · GitHub Models · and more |
| **Enterprise (catalog)** | Amazon Bedrock · Vertex AI *(adapters in progress)* |
| **Anything else** | any OpenAI-compatible or Anthropic-compatible endpoint |

The model registry tracks each model's capabilities, context window, and cost — and the live model picker discovers what your provider actually serves.

## Tools

| Tool | Purpose | Side effect |
|---|---|---|
| `read_file` · `list_directory` · `grep` · `glob` | explore & search | read |
| `web_fetch` | fetch docs & references | network |
| `update_plan` · `ask_user` | plan & clarify | none |
| `write_file` · `edit_file` · `apply_patch` | create & modify | write (gated) |
| `bash` | run shell commands | shell (gated) |
| `Task` · `TaskOutput` · `TaskStop` | delegate to specialist subagents | per-tool gating |
| `GenerateSpecialist` | create new subagent manifests | write (gated) |
| `skill` | load markdown instruction skills | read |
| `tool_search` | lazily load deferred tools (large MCP sets stay cheap) | none |
| `escalate_model` | request a stronger model mid-run | gated by `--allow-escalation` |

Every mutating tool routes through the permission policy **before** any side effect.

### Web search & scraping (free, no API key)

`web_fetch` is built in and always available: it pulls a single URL into clean
markdown locally, with no third party. For **search**, JS-rendered scraping,
whole-site crawls, PDF→markdown, and structured extraction, Zero ships the
[Firecrawl](https://firecrawl.dev) **keyless** MCP server **enabled by default**, so
web search and scraping work out of the box with **no setup and no API key** (1,000
free credits/month). It is a normal MCP server, so you stay in control:

```bash
zero mcp tools list           # see the firecrawl_* tools it exposes
zero mcp disable firecrawl    # turn it off — web_fetch stays as the local floor
```

- **Privacy / disclosure:** keyless requests route through `firecrawl.dev`. If you
  prefer nothing leaves your machine, `zero mcp disable firecrawl` and rely on
  `web_fetch`, or self-host below.
- **Self-host (unlimited, private):** Firecrawl is open source (AGPL-3.0). Run your
  own instance and override the default URL — Zero only *calls* it over the network,
  so the AGPL never reaches into Zero's own code:
  ```jsonc
  // config.json
  { "mcp": { "servers": { "firecrawl": { "type": "http", "url": "http://localhost:3002/v2/mcp" } } } }
  ```
- **Bring your own key (higher limits):** add an auth header over the default:
  ```bash
  zero mcp add firecrawl --type http --url https://mcp.firecrawl.dev/v2/mcp \
    --header "Authorization: Bearer fc-your-key"
  ```

An MCP server that can't be reached at startup (e.g. Firecrawl on an offline
machine) is **skipped with a warning, not fatal** — Zero still launches and the
rest of its tools, including `web_fetch`, keep working.

### Extra write directories (`--add-dir`)

Zero confines writes to the workspace by default. To let the agent write somewhere
else, pass the repeatable `--add-dir` flag — it works for both the interactive TUI
and `zero exec`:

```bash
zero --add-dir ~/Desktop/scratch                       # launch the TUI with an extra write root
zero exec --add-dir ../sibling-repo "update both repos"
```

In the TUI, `/add-dir <path>` grants a directory mid-session (session-only), and a
bare `/add-dir` lists the current write roots. To persist extra roots across
sessions, set `sandbox.additionalWriteRoots` in the **global** user config
(`~/.config/zero/config.json`); the key is deliberately ignored in project config
so a checked-out repo can't widen its own sandbox. Flag and config sources merge
as a union.

Granted roots must already exist (the filesystem root is rejected), symlinks are
resolved when the grant is made, and the same per-root symlink-traversal checks
that protect the workspace apply to each extra root. Relative paths in tool calls
still resolve against the workspace only, and network and destructive-shell policy
are unchanged. A write denied outside all roots returns an error that suggests
`/add-dir`.

Two extra hardening/diagnostic flags are available in the `sandbox` config block,
both **off by default** and safe to leave unset:

- `sandbox.blockUnixSockets` (Linux) — asks the Linux sandbox helper to install a
  best-effort seccomp filter that denies `AF_UNIX` socket creation in the
  sandboxed command. Linux packages also continue to ship `zero-seccomp` as a
  compatibility wrapper for existing direct uses; new sandbox plans do not depend
  on that external wrapper.
- `sandbox.monitorDenials` (macOS) — tails the unified log for this run's seatbelt
  denials and appends them to a command's stderr as a `<sandbox_violations>` block
  so blocked operations are visible. A no-op on OS versions that do not deliver
  seatbelt denials to the queryable log.

When the network policy is `scoped`, the `web_fetch` and `web_search` tools honor
the same allow/deny domain list as sandboxed shell egress (host allowlisting is
enforced on the macOS `sandbox-exec` backend; other backends collapse `scoped` to
`deny` for these tools).

## Architecture

```
   TUI (Bubble Tea)      headless exec       MCP server      cron runner
        └──────────────────────┬──────────────────────┘
                  surface-agnostic agent core
            (loop · typed event stream · tool registry)
   ┌──────────┬──────────┬───────────┬───────────┬────────────┬──────────┐
 providers   tools     sessions    specialist   repo intel   permissions
 + catalog   registry  + search    + background + workspace  + sandbox
 + registry            + rewind      tasks        index      + redaction
```

- **Surface-agnostic core** — the agent loop streams text + tool calls and emits one typed event stream consumed identically by the TUI, `exec`, the MCP server, and cron.
- **Edges are interfaces** — `Provider`, `Tool`, `SessionStore`, and the permission policy are swappable.
- **Model is data** — capabilities, cost, and routing live in the registry, never hard-coded.
- **Pure Go** — one static binary per platform; the npm wrapper just delegates to it.

## Project layout

```
cmd/
  zero/                 production CLI entrypoint
  zero-release/         release builder + smoke tests
  zero-perf-bench/      performance benchmarks
  zero-pr-review/       deterministic PR review helper
internal/
  agent/ zeroruntime/   agent loop & runtime orchestration
  cli/                  command surface (exec, doctor, cron, ...)
  tui/                  Bubble Tea terminal interface
  providers/ providercatalog/ providermodelcatalog/
  modelregistry/        capabilities, context windows, cost
  tools/                read/write/edit/bash/grep/glob/patch/...
  specialist/ background/  subagents + out-of-process tasks
  sessions/ search/     append-only store, full-text search
  repomap/ repoinfo/ workspaceindex/ contextreport/
  specmode/ cron/ skills/ plugins/ hooks/ mcp/
  sandbox/ redaction/ secrets/   safety surfaces
  doctor/ providerhealth/ verify/ selfverify/
docs/                   PRD, protocols, install/update/perf
scripts/                installers
```

## Development

```bash
go test ./...                     # full test suite (200+ test files)
go run ./cmd/zero-release build   # compile the release binary
go run ./cmd/zero-release smoke   # smoke-test it
go run ./cmd/zero-perf-bench      # perf benchmarks (docs/PERFORMANCE.md)

# cross-compile
go run ./cmd/zero-release build --goos linux --goarch amd64
go run ./cmd/zero-release build --goos windows --goarch amd64 --output dist/zero.exe
```

## Documentation

- [Product Requirements (PRD)](docs/PRD.md) — vision, full feature spec, roadmap
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md) — headless I/O contract
- [Specialists](docs/SPECIALISTS.md) — subagent manifests, Task tools, background state
- [Install](docs/INSTALL.md) · [Update](docs/UPDATE.md) · [Performance](docs/PERFORMANCE.md)

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Run `go test ./...` and the relevant build or smoke command before opening a PR.

## License

License is being finalized; a `LICENSE` file will be added before the public release.

---

<div align="center">
<sub><b>Zero</b> — one terminal · every model</sub>
</div>
