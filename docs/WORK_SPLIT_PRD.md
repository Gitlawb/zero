# Zero WorkSplit PRD v2: Balanced And Conflict-Free Team Plan

Status: Draft v2.0
Date: 2026-06-02
Timeline: 6 months to v1.0
Team: Vasanth, Gnanam, Anandan

This v2 replaces the conflict points in the original WorkSplit PRD:

- UI/TUI surfaces are separated from backend capabilities.
- Headless `exec` is split into CLI surface, runtime protocol, and distribution responsibilities.
- Search is scheduled after a minimal session event store exists.
- Slash commands are split into command UI, backend APIs, and integration commands.
- Anandan's workload is narrowed to infra/distribution/integrations, with clear dependencies on Gnanam runtime contracts.
- Each milestone gives all 3 people similar effort and reviewable PR slices.

## Product Goal

Zero is a production coding agent CLI/TUI that can:

- Chat and stream responses in a polished terminal UI.
- Read, write, edit, search, and run tools safely.
- Use OpenAI-compatible, Anthropic, and Gemini providers.
- Track context and cost.
- Run headless for scripts, automation, CI, and VS Code.
- Persist, resume, fork, search, and export sessions.
- Connect to MCP servers and load local plugins.
- Run tests, manage git, self-verify changes, and operate inside sandbox policies.

## Ownership Rules

These rules prevent future conflicts:

1. Every feature has one Directly Responsible Owner.
2. UI/TUI commands and backend modules are separate deliverables.
3. Cross-owner features must land the backend contract before UI/integration work depends on it.
4. Provider-specific logic stays inside provider modules.
5. Session/event protocol is owned by Gnanam and consumed by TUI, VS Code, MCP, and automation.
6. Distribution work packages stable behavior; it does not define runtime semantics.
7. Permission prompts are Vasanth's UI, but grants/policy/sandbox enforcement are Gnanam's backend.
8. Windows platform implementation is Anandan's, but it must use the shared sandbox policy interface.

## Team Roles

| Person | Primary role | Owns | Does not own |
|---|---|---|---|
| Vasanth | Product lead, TUI, tools UX | Bubble Tea TUI, slash command UI, tool result rendering, local tool UX, permission prompts, themes, command palette, user-facing flows | Provider internals, session protocol, MCP transports, release pipeline |
| Gnanam | Runtime backend, providers, protocols | Model registry, provider factory, Anthropic/Gemini, usage/cost, session store, stream-json, config validation, doctor/search backend, MCP/plugin backend, permissions/grants, sandbox policy | TUI rendering, binary packaging, VS Code extension UI |
| Anandan | Infra, distribution, external integrations | CI, build, single binary, release packages, installers, self-update, performance benchmarks, VS Code extension, GitHub PR integration, Windows sandbox, release security | Provider internals, TUI command rendering, model registry |

## Shared Contracts

These contracts are required before dependent work starts:

| Contract | Owner | Consumers | Needed by |
|---|---|---|---|
| Provider stream event schema | Gnanam | Vasanth, Anandan | TUI streaming, headless output, VS Code |
| Model registry API | Gnanam | Vasanth | `/model`, `/effort`, cost, context budget |
| Session event schema | Gnanam | Vasanth, Anandan | `/resume`, `/rewind`, search, VS Code, audit |
| Command registry shape | Vasanth | Gnanam, Anandan | Slash command registration and help |
| Build/test scripts | Anandan | Vasanth, Gnanam | CI, DoD, release |
| Permission/grant policy | Gnanam | Vasanth, Anandan | prompts, MCP permissions, sandbox, Windows |
| Stream-json protocol | Gnanam | Anandan | VS Code extension and automation |

## Milestone M0: Foundation Baseline, Weeks 1-2

Goal: Zero runs locally, has basic tools, one OpenAI-compatible provider, basic TUI, tests, and project scripts.

Current status: the earlier TS/Bun M0 baseline is historical context only. The Go migration resets M0 around a new Go module, `cmd/zero` entrypoint, Bubble Tea TUI shell, core tools, tests, and validation commands. M1 work should start only after these Go foundation PRs are merged.

### Vasanth

- Tool interface with JSON Schema-compatible parameter validation.
- Core file tools: read, write, edit, grep, list directory, bash, plan.
- Basic Bubble Tea TUI and message/tool rendering.
- Basic agent loop integration.
- PR: `feat/go-m0-tools-tui`.

### Gnanam

- Define the first Go provider contract for the M0 OpenAI-compatible provider.
- Define first draft of normalized `Usage` type.
- Define first draft of model registry type.
- Review config loader and list missing provider/config fields.
- PR: `feat/go-m0-runtime-contracts`.

### Anandan

- Add stable project commands: `go test ./...`, `go build ./cmd/zero`, and the npm wrapper smoke checklist.
- Add initial CI smoke job.
- Verify Go toolchain version, module cache behavior, and npm wrapper lockfile behavior.
- Add build artifact smoke check.
- PR: `feat/m0-ci-scripts`.

### M0 Done

- `go test ./...` passes.
- `go build ./cmd/zero` passes.
- npm wrapper smoke checklist exists when npm packaging files are touched.
- `zero` can run, read a file, edit a file, and stream a response.

### npm Wrapper Smoke Checklist

When npm distribution files are changed, the smoke check must verify:

- `package.json` exists with the expected package name and version.
- `npm ci` succeeds from the wrapper package directory.
- The wrapper binary resolves through the package `bin` entry and `node_modules/.bin`.
- `zero --version` or `zero --help` exits 0 and reports the expected version or command surface.
- The designated npm smoke/build script exits 0.

## Milestone M1: Multi-Provider And Headless Foundation, Weeks 3-4

Goal: Zero can select models/providers, use Claude/Gemini/OpenAI-compatible providers, and run headless with stable output.

### Vasanth: TUI And CLI Surface

- Model selector UI and `/model` command shell.
- Reasoning effort UI and `/effort` command shell.
- Header/footer model display.
- Streaming output polish in TUI.
- Headless command surface: parse `zero exec`, flags, output mode selection.
- Exit code mapping display and CLI user messages.
- PRs:
  - `feat/m1-model-selector-ui`
  - `feat/m1-headless-cli-surface`

### Gnanam: Provider Runtime

- Model registry with 10+ models, cost, context limits, capabilities.
- Provider factory that resolves model/profile/provider.
- Anthropic provider with streaming text, tool calls, system prompt, usage.
- Gemini provider with streaming text, tool calls, usage, vision-ready input shape.
- Normalized provider event schema and usage type.
- PRs:
  - `feat/m1-model-registry`
  - `feat/m1-provider-factory`
  - `feat/m1-anthropic-provider`
  - `feat/m1-gemini-provider`

### Anandan: Build And CI

- Single binary build spike with Go cross-compilation.
- CI matrix for Linux/macOS/Windows test and build smoke.
- Release packaging formats: tar.gz for Unix, zip for Windows.
- Artifact upload on tags or manual workflow.
- PRs:
  - `feat/m1-ci-matrix`
  - `feat/m1-binary-build`
  - `feat/m1-release-packaging`

### M1 Dependency Order

1. Gnanam lands model registry.
2. Gnanam lands provider factory.
3. Vasanth connects model selector to registry.
4. Gnanam lands Anthropic/Gemini.
5. Vasanth finishes provider-aware TUI/headless surface.
6. Anandan packages once scripts and entrypoints are stable.

### M1 Done

- `zero exec -m <model> "list files"` runs through provider factory.
- OpenAI-compatible provider still works.
- Anthropic and Gemini providers pass mocked stream tests.
- Model selector reads registry data.
- CI runs tests and build checks on PR.
- Binary build has at least one working platform artifact or documented blocker.

## Milestone M2: Core Commands, Cost, Observability, Install Flow, Weeks 5-8

Goal: Core slash commands work, cost and diagnostics are visible, sessions have a minimal event store, and installation/update flows begin.

### Vasanth: Core Command UX

- Slash command framework and help registry.
- Session commands UI: `/clear`, `/compact`, `/context`, `/resume`, `/exit`.
- Model commands UI: `/model`, `/effort`, `/style`.
- Meta commands UI: `/help`, `/config`, `/doctor`.
- Tool result truncation and pagination UI.
- Auto-compaction prompt UX.
- PRs:
  - `feat/m2-slash-framework`
  - `feat/m2-session-model-commands`
  - `feat/m2-tool-truncation`
  - `feat/m2-compaction-ui`

### Gnanam: Observability Backend

- Minimal session event store to support cost/search/resume later.
- Cost tracking from normalized usage and model registry.
- `zero doctor` backend checks: binary, config, provider, connectivity, model validity.
- `zero search` backend over local session events.
- Config inspection and validation API for `/config`.
- Secret redaction helper used by logs, provider errors, doctor, feedback.
- PRs:
  - `feat/m2-minimal-session-events`
  - `feat/m2-cost-tracking`
  - `feat/m2-doctor`
  - `feat/m2-search`
  - `feat/m2-secret-redaction`

### Anandan: Install, Update, Performance

- Self-update design and `zero update --check`.
- Install scripts for Linux/macOS and PowerShell draft for Windows.
- Performance benchmark harness: cold start, TTFT, memory.
- CI performance smoke job with threshold warnings.
- Release checksum generation.
- PRs:
  - `feat/m2-update-check`
  - `feat/m2-install-scripts`
  - `feat/m2-perf-bench`
  - `feat/m2-release-checksums`

### M2 Command Scope

Fully working in M2:

- `/clear`
- `/compact`
- `/context`
- `/resume`
- `/exit`
- `/model`
- `/effort`
- `/style`
- `/help`
- `/config`
- `/doctor`

Registered but full backend lands later:

- `/rewind`: full in M3 with session event replay.
- `/permissions`: full in M4/M6 with permission/grant system.
- `/init`, `/memory`, `/skills`: full in M4 with memory/skills.
- `/mcp`, `/hooks`, `/agents`: full in M4 with MCP/hooks/subagents.
- `/feedback`: full in M3/M5 with integration/redaction.

### M2 Done

- TUI command framework is stable.
- Core 11 commands work.
- Cost shows in footer/session summary.
- `zero doctor` gives redacted pass/warn/fail checks.
- `zero search` can search persisted local events.
- Install scripts and update check have smoke tests.

## Milestone M3: Sessions, Stream Protocol, VS Code MVP, Weeks 9-10

Goal: Sessions are durable, stream-json is stable, and VS Code can drive Zero through the protocol.

### Vasanth: TUI Session UX

- Themes: light, dark, custom config.
- Mouse support for selectors and command palette where feasible.
- Collapsible tool rows with one-line summaries.
- `/rewind` interactive UI using Gnanam's session event API.
- Session picker polish for `/resume` and fork display.
- PRs:
  - `feat/m3-themes`
  - `feat/m3-tool-rows`
  - `feat/m3-rewind-ui`

### Gnanam: Headless And Sessions

- Full session persistence under `~/.local/share/zero/sessions/<id>/`.
- Append-only `events.jsonl`.
- `--resume` and `--fork` support.
- Stream-json input/output protocol with schema tests.
- Config validation errors with source paths.
- Session search indexing improvements.
- PRs:
  - `feat/m3-sessions`
  - `feat/m3-resume-fork`
  - `feat/m3-stream-json`
  - `feat/m3-config-validation`

### Anandan: VS Code And Docs Distribution

- VS Code extension MVP using stream-json.
- Inline chat panel.
- Diff view integration.
- Homebrew tap or formula draft.
- Documentation site skeleton with install and CLI docs.
- PRs:
  - `feat/m3-vscode-mvp`
  - `feat/m3-homebrew`
  - `feat/m3-docs-site`

### M3 Dependency Order

1. Gnanam freezes stream-json event schema.
2. Anandan builds VS Code against that schema.
3. Gnanam lands session resume/fork.
4. Vasanth wires `/rewind` and session picker.
5. Anandan documents protocol and install path.

### M3 Done

- Sessions survive process restart.
- `zero exec --resume <id>` works.
- `zero exec --fork <id>` creates a new session branch.
- Stream-json is line-delimited and schema-tested.
- VS Code MVP can send prompt and render streamed text/tool events.

## Milestone M4: Extensibility, MCP, Skills, Subagents, Weeks 11-14

Goal: Users can extend Zero with memory, skills, hooks, MCP servers, plugins, and subagents.

### Vasanth: Extensibility UX

- `/init` generates project memory scaffold.
- `/memory` inspect/reload UI.
- `/skills` manager UI.
- `/hooks` manager UI.
- `/mcp` manager UI shell.
- `/agents` selector UI.
- PRs:
  - `feat/m4-memory-ui`
  - `feat/m4-skills-ui`
  - `feat/m4-extensibility-commands`

### Gnanam: MCP And Plugin Backend

- MCP client with stdio, HTTP, and SSE transports.
- Surface MCP tools in Zero tool registry.
- MCP permissions: per-server and per-tool grants, list/revoke/clear.
- Local plugin loader from `~/.config/zero/plugins/` and `.zero/plugins/`.
- Plugin manifest validation with `plugin.json`.
- MCP server mode: `zero serve --mcp`.
- Hook config storage and audit events.
- PRs:
  - `feat/m4-mcp-client`
  - `feat/m4-mcp-permissions`
  - `feat/m4-plugin-loader`
  - `feat/m4-mcp-server`
  - `feat/m4-hook-backend`

### Anandan: Subagents And Integration Docs

- Subagent framework using Gnanam's parent/child session linkage.
- `task` tool implementation for scoped subagents.
- Built-in agents: code-review, security-review, refactor, test-gen.
- VS Code support for subagent session tree.
- Extensibility docs for MCP/plugins/agents.
- PRs:
  - `feat/m4-subagent-framework`
  - `feat/m4-builtin-agents`
  - `feat/m4-vscode-agents`
  - `feat/m4-extensibility-docs`

### M4 Dependency Order

1. Gnanam lands MCP tool registry integration.
2. Vasanth wires `/mcp` UI to backend.
3. Gnanam lands parent/child session APIs.
4. Anandan lands subagent framework.
5. Vasanth wires `/agents` UI.

### M4 Done

- User can add and call an MCP tool.
- User can list/revoke/clear MCP permissions.
- User can load a local plugin manifest.
- User can run `zero serve --mcp`.
- User can spawn a scoped subagent through `task`.
- Extensibility commands are fully functional.

## Milestone M5: Advanced Agent Operations, Weeks 15-20

Goal: Zero can verify work, manage git/test flows, and assist code review.

### Vasanth: Advanced Tool UX

- `web_fetch` and `web_search` tools with safe display and truncation.
- LSP diagnostics display in TUI.
- Typecheck/lint result rendering.
- Rich test failure rendering in TUI.
- Review summary UI for agent findings.
- PRs:
  - `feat/m5-web-tools-ui`
  - `feat/m5-diagnostics-ui`
  - `feat/m5-verification-ui`

### Gnanam: Git And Verification Backend

- Git worktree isolation per task.
- Auto-commit backend: detect changes and generate commit messages.
- Test runner detection and execution.
- Parse test results into structured summaries.
- Self-verification loop: run checks after edits, retry within budget.
- PRs:
  - `feat/m5-worktrees`
  - `feat/m5-autocommit`
  - `feat/m5-test-runner`
  - `feat/m5-self-verify`

### Anandan: GitHub And Review Automation

- GitHub API integration for PR metadata and comments.
- Automated code-review workflow using built-in agent.
- Security-review workflow.
- Dependency update detection.
- CI integration for agent-generated reports.
- PRs:
  - `feat/m5-github-integration`
  - `feat/m5-code-review-agent`
  - `feat/m5-security-review-agent`
  - `feat/m5-dependency-updates`

### M5 Dependency Order

1. Gnanam lands git/test backend APIs.
2. Vasanth renders verification output.
3. Anandan builds GitHub PR review workflow on git/test/session APIs.
4. Anandan publishes CI report integration.

### M5 Done

- Zero can create/use a git worktree for a task.
- Zero can run project tests and parse failures.
- Zero can self-verify after edits.
- Zero can produce a code-review report.
- GitHub PR comment posting works when configured.

## Milestone M6: Sandbox And Security, Weeks 21-24

Goal: Zero can safely run higher-autonomy workflows with OS-aware sandboxing and durable permission grants.

### Vasanth: Security UX

- Permission prompts: allow, deny, always allow, always deny.
- Autonomy level UI: low, medium, high.
- Permission audit log view.
- Sandbox violation rendering.
- Security warnings in TUI and headless text output.
- PRs:
  - `feat/m6-permission-prompts`
  - `feat/m6-autonomy-ui`
  - `feat/m6-audit-ui`

### Gnanam: Sandbox Backend

- Shared sandbox policy interface.
- Linux sandbox using bubblewrap.
- macOS sandbox using sandbox-exec or best available policy backend.
- Persistent grants: allow/deny list/revoke/clear.
- Network and filesystem policy enforcement.
- Structured sandbox violation errors.
- PRs:
  - `feat/m6-sandbox-policy`
  - `feat/m6-sandbox-linux`
  - `feat/m6-sandbox-macos`
  - `feat/m6-persistent-grants`

### Anandan: Windows And Release Security

- Windows sandbox using AppContainer or best supported Windows isolation.
- Release signing/checksum verification.
- Security CI hardening.
- Dependency vulnerability scanning.
- Security audit and penetration test coordination.
- PRs:
  - `feat/m6-sandbox-windows`
  - `feat/m6-release-signing`
  - `feat/m6-security-ci`
  - `feat/m6-security-audit`

### M6 Dependency Order

1. Gnanam lands shared sandbox policy interface.
2. Vasanth wires prompts to the shared policy.
3. Gnanam implements Linux/macOS policy adapters.
4. Anandan implements Windows adapter using same policy.
5. Anandan finalizes release security.

### M6 Done

- `zero --auto high` uses permission policy and sandbox backend.
- Persistent grants work across sessions.
- Linux/macOS sandboxing works or has documented OS-specific fallback.
- Windows sandbox adapter works or has documented fallback.
- Security audit findings are triaged and fixed or accepted.

## Slash Command Ownership Matrix

| Command | UI owner | Backend owner | Fully done by | Notes |
|---|---|---|---|---|
| `/clear` | Vasanth | Gnanam | M2 | New session/reset event. |
| `/compact` | Vasanth | Gnanam | M2 | Requires token/cost/context backend. |
| `/context` | Vasanth | Gnanam | M2 | Registry + usage + tool/MCP context. |
| `/rewind` | Vasanth | Gnanam | M3 | Requires durable event store. |
| `/resume` | Vasanth | Gnanam | M3 | M2 can list minimal sessions; M3 full resume/fork. |
| `/exit` | Vasanth | Gnanam | M2 | Must flush session-end event. |
| `/model` | Vasanth | Gnanam | M1/M2 | Registry lands M1, UI polished M2. |
| `/effort` | Vasanth | Gnanam | M1/M2 | Reasoning metadata from registry. |
| `/style` | Vasanth | Vasanth | M2 | UI/config preference only unless style affects prompts. |
| `/permissions` | Vasanth | Gnanam | M4/M6 | MCP permissions in M4, sandbox grants in M6. |
| `/init` | Vasanth | Vasanth | M4 | Project memory scaffold. |
| `/memory` | Vasanth | Vasanth | M4 | Memory inspect/reload. |
| `/skills` | Vasanth | Gnanam | M4 | UI by Vasanth; plugin-provided skills backend by Gnanam. |
| `/mcp` | Vasanth | Gnanam | M4 | MCP client/server/backend by Gnanam. |
| `/hooks` | Vasanth | Gnanam | M4 | UI by Vasanth; hook config/audit backend by Gnanam. |
| `/agents` | Vasanth | Anandan | M4 | Requires Gnanam parent/child session API. |
| `/help` | Vasanth | Vasanth | M2 | Command metadata registry. |
| `/config` | Vasanth | Gnanam | M2/M3 | Inspect in M2, validation source errors in M3. |
| `/doctor` | Vasanth | Gnanam | M2 | Backend CLI and TUI share same checks. |
| `/feedback` | Vasanth | Anandan | M3/M5 | Redaction by Gnanam; GitHub/product integration by Anandan. |

## Headless Exec Ownership

Headless `exec` is split to avoid conflict:

| Area | Owner | Scope |
|---|---|---|
| CLI command and flags | Vasanth | `zero exec`, `--model`, `--output-format`, help text, exit message display. |
| Runtime execution | Gnanam | Provider factory, stream events, usage, tool loop hooks, session recording, stream-json. |
| Packaging and CI | Anandan | Binary command works across platforms, CI tests headless modes, release artifacts. |

## Permission Ownership

| Area | Owner | Scope |
|---|---|---|
| Prompt UI | Vasanth | Allow/deny/always controls and TUI display. |
| Policy engine | Gnanam | Risk classification, grants, persistent decisions, MCP permissions, sandbox policy. |
| Platform adapters | Gnanam + Anandan | Gnanam owns Linux/macOS; Anandan owns Windows using same policy. |

## Definition Of Done Per PR

Every PR must include:

- Tests for changed behavior.
- `go test ./...` must pass when the PR modifies any Go code; docs-only PRs are exempt and must say so in the PR description.
- `go build ./cmd/zero` must pass when the PR modifies `cmd/zero` or runtime packages used by the entrypoint; docs-only and npm-wrapper-only PRs are exempt and must say so in the PR description.
- The npm wrapper smoke checklist must pass when the PR modifies npm distribution files.
- PR description explains what changed and why.
- No unrelated refactors.
- Redaction used for secrets in logs/errors/tests.
- Cross-owner API changes include a small contract note or test.

## Review Rotation

| Author | Primary reviewer | Secondary reviewer when cross-owner |
|---|---|---|
| Vasanth | Gnanam | Anandan for distribution/VS Code impact |
| Gnanam | Anandan | Vasanth for TUI/UX impact |
| Anandan | Vasanth | Gnanam for runtime/protocol impact |

## Balanced Workload Summary

| Milestone | Vasanth load | Gnanam load | Anandan load |
|---|---|---|---|
| M0 | Core tools + TUI baseline | Runtime contracts | Scripts + CI baseline |
| M1 | Model/headless UI | Registry + providers | CI + binary + packaging |
| M2 | Core slash UX + compaction UI | Cost + doctor + search + events | Install + update + perf |
| M3 | Session UI + themes | Sessions + stream-json | VS Code + docs + Homebrew |
| M4 | Extensibility UI | MCP + plugins + permissions | Subagents + agent docs |
| M5 | Verification/web UX | Git + tests + self-verify | GitHub review automation |
| M6 | Security UX | Linux/macOS sandbox + grants | Windows sandbox + release security |

This keeps workload balanced while preserving clean ownership boundaries.

## Immediate Next Steps

1. Reset M0 for the Go migration and treat old TS/Bun PR #6 as historical only.
2. Anandan starts `feat/go-m0-module-entrypoint` with `go.mod`, `cmd/zero`, `go test ./...`, and `go build ./cmd/zero`.
3. Gnanam starts `feat/go-m0-runtime-contracts` for the Go provider/tool/runtime contracts.
4. Vasanth starts `feat/go-m0-tools-tui` for core tools, Bubble Tea startup shell, and basic agent loop integration.
5. Anandan adds CI smoke coverage for the Go commands and the npm wrapper smoke checklist when wrapper files land.
6. Start M1 model registry, provider factory, and model selector work only after the Go M0 foundation PRs are merged.

