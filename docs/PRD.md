# Zero — Product Requirements Document (v1.0)

**Status:** DRAFT v1.0
**Target:** Compete with Claude Code + Cursor + other multi-provider terminal agents
**Timeline:** 6-12 months
**Stack:** Go native core + npm distribution wrapper

---

## 1. Vision

**Zero is a terminal-first, editor-aware, multi-provider AI coding platform.**

It combines the best of:
- **Claude Code** — terminal-native agentic coding
- **Cursor** — IDE integration + smart editing
- **Multi-provider terminal agents** — provider-agnostic + headless

**One Go-native agent core. Three surfaces. Zero lock-in.**

---

## 2. Core Surfaces

### 2.1 Terminal (TUI) — Primary
- Interactive coding agent in terminal
- Streaming, append-style (like Claude Code)
- Bubble Tea + Lip Gloss-based
- Full keyboard control
- Mouse support

### 2.2 Editor (VS Code Extension)
- Same agent core, different UI
- Inline suggestions + chat panel
- File editing with diff view
- Diagnostics integration
- LSP-aware

### 2.3 Headless (CLI/CI)
- `zero exec "prompt"` for scripts
- `zero serve` for daemon mode
- JSON-RPC over stdio/WebSocket
- MCP server support

---

## 3. Architecture

```
┌─────────────────────────────────────────────────┐
│              ZERO PLATFORM                        │
├─────────────────────────────────────────────────┤
│                                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐     │
│  │   TUI    │  │   VSCode │  │ Headless │     │
│  │ Surface  │  │ Surface  │  │ Surface  │     │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘     │
│       │             │             │            │
│       └─────────────┼─────────────┘            │
│                     │                          │
│              ┌──────▼──────┐                   │
│              │  Agent Core │                   │
│              │  (ReAct)    │                   │
│              └──────┬──────┘                   │
│                     │                          │
│  ┌──────┬──────┬────┴────┬──────┬──────┐      │
│  │Tools │Perms │Sessions │Provi │Memory│      │
│  └──────┴──────┴─────────┴──────┴──────┘      │
│                                                  │
└─────────────────────────────────────────────────┘
```

**Layering Rules:**
- Agent Core never imports a surface
- Surfaces are thin shells (I/O + rendering)
- All state persists to local files
- No cloud dependency

---

## 4. Functional Requirements (24 Features)

### F1 — Agent Core (P0)
- ReAct loop: think → tool → result → think
- Stream text + tool calls + thinking
- Parallel tool execution where safe
- Bound turns (max 100)
- Clean interrupt (Esc/Ctrl-C)
- Event stream: `text | tool_call | tool_result | thinking | usage | plan_update | error | turn_end`

### F2 — Tools (P0)

**Core (8):**
- `read_file` — read with line ranges, images
- `write_file` — create/overwrite
- `edit_file` — exact-string edit (uniqueness guard)
- `bash` — run shell command
- `grep` — ripgrep content search
- `glob` — file pattern matching
- `apply_patch` — multi-hunk patches
- `update_plan` — todo list

**Advanced (4):**
- `web_fetch` / `web_search` — web access
- `task` — spawn subagent
- `structured_output` — JSON-Schema validated
- `notebook_edit` — Jupyter notebooks

**Tool Requirements:**
- Go typed parameter schemas -> JSON Schema
- Side-effect class: `read | write | shell | network | out_of_workspace`
- Structured results: `{ status, output, truncated?, meta? }`
- Atomic execution
- Permission gating

### F3 — Providers & Model Registry (P0)
- OpenAI-compatible (works with OpenAI, OpenRouter, Ollama, etc.)
- Anthropic (Claude)
- Google Gemini
- Azure OpenAI
- AWS Bedrock
- xAI (Grok)

**Model Registry:**

```go
type ModelEntry struct {
  ID              string
  Name            string
  Provider        string
  APIProviders    []string
  ContextLimits   ContextLimits
  ReasoningEffort ReasoningEffort
  Capabilities    ModelCapabilities
  Cost            ModelCost
  Tier            string
  MatchPatterns   []string
}
```

- Per-task model selection (`-m`)
- Provider rotation/failover
- Cost tracking
- "Spec model" for planning
- Fallback model on failure

### F4 — Terminal UI (P0)
- **Layout:** header (cwd · git · model) → tool rows → diff → response → input → footer
- **Streaming:** append-only transcript model with live tail updates
- **Markdown:** full CommonMark + syntax highlighting
- **Tool rows:** collapsible with one-line summaries
- **Input:** `/` commands, `@` files, `!` bash, `Esc` interrupt
- **Robustness:** resize, wide-char, ANSI, non-TTY fallback
- **Splash:** ZERO wordmark
- **Themes:** light/dark + custom

### F5 — Sessions (P0)
- Persist to `~/.local/share/zero/sessions/<id>/`
- `meta.json` + `events.jsonl` + `messages.jsonl` + `checkpoints/`
- `--resume [id]` (default: latest)
- `--fork <id>` (copy with lineage)
- Search: `zero search <query>` over messages/tools/results
- Atomic writes (write-temp-then-rename)
- Schema versioning
- Export to Markdown/JSON

### F6 — Configuration (P0)
- Layered: defaults → user → project → env → CLI
- `~/.config/zero/config.json` (user)
- `./.zero/config.json` (project)
- Runtime overlay (`--settings <path>`)
- Feature flags for staged rollout
- Validation on load

### F7 — MCP (Model Context Protocol) (P0)
- Connect to stdio/HTTP/SSE servers
- Surface MCP tools into registry
- `zero mcp add/remove/list`
- Per-server and per-tool permissions
- Persistent grants with revoke/clear
- MCP server mode (zero as MCP server)

### F8 — Headless / Programmatic (P0)
- `zero exec [prompt]` — one-shot
- `zero serve` — daemon mode
- `--input-format stream-json` — line-delimited events
- `--input-format stream-jsonrpc` — JSON-RPC protocol
- `-o text|json` output formats
- Exit codes: `0=success, 1=crash, 2=usage, 3=provider, 4=tool, 5=permission, 6=budget`

### F9 — Permissions & Autonomy (P0)
- **Side-effect classes:** read, write, shell, network, out_of_workspace
- **Autonomy levels:** low (ask), medium (smart), high (auto)
- `--skip-permissions-unsafe` (loud warning)
- TUI: inline prompts (allow/deny/always)
- Headless: default-deny unless granted
- Persistent grants with list/revoke/clear
- Audit log in `events.jsonl`

**Permission Matrix:**

| Tool Class | low | medium | high |
|------------|-----|--------|------|
| Read | allow | allow | allow |
| Workspace write | prompt | allow | allow |
| Shell | prompt | prompt | allow |
| Network | prompt | allow | allow |
| Out-of-workspace | deny | prompt | prompt |

### F10 — Observability (P0)
- Structured logging (stdout/stderr split)
- Secret redaction (API keys, tokens, passwords)
- No vendor telemetry (local only)
- `zero doctor` — health check command
- Crash reports (opt-in, local)
- Performance metrics (TTFT, tokens/sec)

### F11 — Subagents (P1)
- `task` tool spawns scoped subagent
- Built-in agents: code-review, security-review, refactor, test-gen
- Recursion depth limit
- Parent session linkage
- Shared/isolated budgets

### F12 — Plugins (P1)
- User/project-scoped plugins
- Commands, tools, prompts, hooks
- Plugin manifest: `plugin.json`
- `zero plugin install <path|url>`
- No marketplace in v1 (manual install)

### F13 — Distribution (P0)
- **Single binary** via Go cross-compilation
- npm package as a thin installer/launcher for the platform binary
- Cross-platform: Linux, macOS, Windows
- `zero update [--check]` self-update
- Checksum verification
- Rollback on failure

### F14 — VS Code Extension (P1)
- Same agent core via stream protocol
- Inline chat panel
- Diff view in editor
- Diagnostics integration
- LSP awareness
- File watching

### F15 — Memory & Context (P1)
- `AGENTS.md` hierarchical memory
- `@import` for modular rules
- Auto-generated from `init` command
- Project conventions learning
- Skills system (reusable instruction packs)
- Context budgeting (token tracking)
- Auto-compaction when full

### F16 — Hooks System (P1)
- Lifecycle hooks: `PreToolUse`, `PostToolUse`, `SessionStart`, `SessionEnd`
- User-defined shell commands
- JSON output for conditional flow
- `.zero/hooks/` directory

### F17 — Slash Commands (P0)

**Built-in (21):**
- Session: `/clear`, `/compact`, `/context`, `/add-dir`, `/rewind`, `/resume`, `/exit`
- Model: `/model`, `/effort`, `/style`, `/permissions`
- Project: `/init`, `/memory`, `/skills`
- Extensibility: `/mcp`, `/hooks`, `/agents`
- Meta: `/help`, `/config`, `/doctor`, `/feedback`

**Custom:** `./.zero/commands/*.md`

### F18 — Git Integration (P1)
- Worktree isolation per task
- Auto-commit with messages
- Branch management
- PR creation
- Diff review
- Conflict resolution

### F19 — Testing & Verification (P1)
- `run_tests` tool
- Self-verification loop
- Test runner integration
- Coverage reporting
- Diagnostics (typecheck, lint)
- LSP integration

### F20 — Web Access (P1)
- `web_fetch` — fetch URL content
- `web_search` — search engine
- Provider/permission gated
- Rate limiting
- Caching

### F21 — Sandbox (P2)
- Linux: bubblewrap
- macOS: sandbox-exec
- Windows: AppContainer
- Filesystem policies (workspace write confinement + user-granted extra roots via `--add-dir`)
- Network policies
- Unix socket controls

### F22 — Multi-modal (P2)
- Image input (screenshots, diagrams)
- PDF parsing
- Voice input (optional)
- File attachments
- Paste support

### F23 — Telemetry (P2)
- Local-only metrics
- Token usage tracking
- Cost reports
- Performance profiling
- Crash reports (opt-in)
- NO vendor telemetry

### F24 — Advanced Features (P2)
- Code review automation
- Auto-refactor
- Dependency updates
- Security scanning
- Documentation generation

---

## 5. Data Models

### 5.1 Headless Event Stream

```json
{ "type": "run_start", "runId": "r1", "sessionId": "s1", "cwd": "/repo", "model": "..." }
{ "type": "text", "delta": "..." }
{ "type": "tool_call", "id": "t1", "name": "bash", "args": {...}, "sideEffect": "shell" }
{ "type": "permission", "id": "t1", "decision": "needs_approval", "reason": "shell" }
{ "type": "tool_result", "id": "t1", "status": "ok", "output": "...", "truncated": false }
{ "type": "usage", "promptTokens": 1234, "completionTokens": 567, "costUsd": 0.012 }
{ "type": "plan_update", "plan": [{ "id": "1", "content": "...", "status": "in_progress" }] }
{ "type": "turn_end", "stopReason": "end_turn" }
{ "type": "error", "code": "rate_limit", "message": "...", "recoverable": true }
{ "type": "run_end", "status": "success", "exitCode": 0 }
```

### 5.2 Session On Disk

```
~/.local/share/zero/sessions/<id>/
  meta.json        # { schemaVersion, id, parentId?, title, cwd, model, createdAt, updatedAt, tokens, cost }
  events.jsonl     # append-only AgentEvent stream
  messages.jsonl   # provider-facing messages
  checkpoints/     # snapshots for restore/fork/compact
```

### 5.3 Tool Contract

```go
type Tool interface {
  Name() string
  Description() string
  Parameters() JSONSchema
  SideEffect() SideEffect
  Execute(ctx ToolContext, args map[string]any) ToolResult
}

type ToolResult struct {
  Status    string
  Output    string
  Truncated bool
  Meta      map[string]any
}
```

### 5.4 Config Schema

```json
{
  "model": "claude-sonnet-4-6",
  "providers": {
    "anthropic": { "apiKey": "env:AN..._KEY" },
    "openai": { "apiKey": "env:OP..._KEY" }
  },
  "permissions": {
    "autonomy": "medium",
    "grants": [{ "tool": "bash", "pattern": "git status", "decision": "allow" }]
  },
  "ui": { "theme": "dark", "syntaxHighlighting": true }
}
```

---

## 6. Non-Functional Requirements

### Performance
- TTFT < 500ms after provider's first byte
- No full-transcript repaint per token
- Cold start < 300ms (warm cache)
- Tool execution < 100ms overhead
- Session resume < 1s

### Reliability
- Crash-free TUI under resize, wide-char, non-TTY
- Clean interrupt (no half-written state)
- Atomic session writes
- Recovery from malformed JSONL
- Provider failover

### Security
- No mutation without permission decision (hard invariant)
- API keys never logged
- Secret redaction everywhere
- Headless stdout free of logs
- Workspace boundary by default
- Sandboxing for untrusted code

### Portability
- Linux, macOS, Windows
- Any OpenAI-compatible endpoint
- x86_64, ARM64
- Single binary distribution

### Testability
- Mock provider for unit tests
- Fake MCP server/client
- Fake terminal/PTY
- Sandbox simulator
- Deterministic event replay

---

## 7. Milestones

### M0 — Foundation (Weeks 1-2)
- Go module setup
- Agent core skeleton
- 8 core tools (read, write, edit, bash, grep, glob, apply_patch, update_plan)
- OpenAI-compatible provider
- Basic Bubble Tea TUI
- Permission gate (read vs mutate)
- Session persistence

**Exit:** `zero` runs, can read/edit files, basic chat works.

### M1 — Multi-Provider (Weeks 3-4)
- Anthropic provider
- Gemini provider
- Model registry
- Per-task model selection
- Provider rotation
- Cost tracking
- Headless `exec` mode

**Exit:** Switch between Claude/GPT/Gemini, see cost, run headless.

### M2 — Polish (Weeks 5-8)
- Advanced TUI features
- Full markdown + syntax highlighting
- Tool result truncation/pagination
- Auto-compaction
- Context budgeting
- 20 slash commands
- Doctor command
- Search command

**Exit:** Production-ready TUI, smooth streaming, all commands work.

### M3 — Distribution (Weeks 9-10)
- Single binary build
- Self-update
- Cross-platform CI
- VS Code extension MVP
- Documentation site

**Exit:** `zero` ships as one binary, works on Win/Mac/Linux.

### M4 — Extensibility (Weeks 11-14)
- MCP client + permissions
- Hooks system
- Skills system
- Memory (AGENTS.md)
- Plugin loading
- Subagents (built-in: review, security)

**Exit:** Plugins work, MCP servers connect, memory persists.

### M5 — Advanced (Weeks 15-20)
- Web fetch/search
- Git worktrees
- Self-verification
- Diagnostics (LSP, typecheck)
- Testing integration
- Code review automation

**Exit:** Zero can review code, run tests, manage git.

### M6 — Sandbox & Security (Weeks 21-24)
- Linux sandbox (bubblewrap)
- macOS sandbox
- Windows AppContainer
- Network policies
- Filesystem policies

**Exit:** Untrusted code runs safely in sandbox.

### Beyond v1.0
- Multi-modal (images, PDFs)
- Voice input
- Cloud sync (optional)
- Team features
- Marketplace (if demand)

---

## 8. Risks & Mitigations

| # | Risk | Mitigation |
|---|------|------------|
| R1 | Scope creep | Stick to milestones, defer features |
| R2 | Provider API changes | Abstract via interface, test with mocks |
| R3 | TUI complexity | Use Bubble Tea/Lip Gloss, follow mature terminal agent UX patterns |
| R4 | Performance | Benchmark early, optimize streaming |
| R5 | Security vulnerabilities | Permission gate from day 1, security review |
| R6 | Cross-platform bugs | CI on Win/Mac/Linux from M0 |
| R7 | Breaking changes | Schema versioning, migration tools |

---

## 9. Success Metrics

### v1.0 (6 months)
- 5+ providers supported
- 12+ tools
- 100% test coverage on core
- < 300ms cold start
- Single binary < 50MB
- Works on Win/Mac/Linux
- 1000+ GitHub stars
- 50+ contributors

### v2.0 (12 months)
- VS Code extension published
- MCP server mode
- Plugin ecosystem (20+ plugins)
- 10K+ active users
- Compete with Claude Code on benchmarks

---

## 10. Out of Scope (v1.0)

- Cloud sync / hosted backend
- Relay / remote SSH execution
- Multi-agent mission mode
- Plugin marketplace
- Auto-generated wikis
- Git authorship notes
- Vendor telemetry
- Voice input
- Mobile support

---

## 11. Open Decisions

1. **TUI library:** Bubble Tea + Lip Gloss — **Decide: Go-native TUI** (single-binary friendly, mature terminal ecosystem)
2. **Storage:** JSONL files vs SQLite — **Decide: JSONL** (inspectable, simple)
3. **Package structure:** Single vs monorepo — **Decide: Single** until M4
4. **Provider abstraction:** Direct API vs wrapper — **Decide: Direct** (portability)
5. **Patch strategy:** apply_patch vs edit_file — **Decide: Both** (apply_patch for bulk, edit_file for small)

---

## 12. Team & Timeline

**Team (3 people):**
- You (lead + TUI + tools)
- Backend dev (providers + core)
- Designer (VS Code extension + docs)

**Timeline:**
- M0-M1: 4 weeks (foundation)
- M2-M3: 6 weeks (polish + distribution)
- M4-M5: 8 weeks (extensibility)
- M6: 4 weeks (sandbox)
- **Total v1.0: 6 months**

---

## 13. Why This Will Work

1. **Proven patterns** — Based on clean-room analysis of existing terminal coding-agent UX patterns
2. **Multi-provider** — Unique advantage vs Claude Code/Cursor
3. **Open source** — Community contributions
4. **Go** — Native binary, fast startup, strong concurrency, cross-platform CLI ergonomics
5. **npm distribution wrapper** — Keeps JavaScript ecosystem installation while the runtime stays native
6. **Clear milestones** — Ship early, iterate

---

**This is the real PRD. 13 sections, 24 features, 6 months to v1.0.**
