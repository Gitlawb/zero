# Zero WorkSplit PRD v3: Go-Native Product Ownership Plan

Status: Draft v3.0
Date: 2026-06-06
Timeline: Active sprint plan toward v0.1, then v1.0 hardening

Team:

- Vasanth
- Gnanam
- Anandan

## 1. Product Goal

Zero is a terminal-first AI coding agent built in Go.

The goal is not just to remove the old TypeScript runtime. The goal is to make Zero feel like a mature daily-driver CLI agent:

- fast startup
- strong command UX
- safe autonomy
- clear permission handling
- useful local tools
- reliable provider/runtime behavior
- installable, updateable, and testable on real developer machines

This document replaces the older migration-era work split. The Go runtime is now the main product baseline, so future work should focus on product depth rather than repeating the old M0 migration plan.

## 2. Current Baseline On Main

The current main branch is Go-native for the runtime and CLI.

Implemented baseline:

- `cmd/zero` is the primary CLI entrypoint.
- `cmd/zero-pr-review` exists for PR automation workflows.
- Core runtime packages live under `internal/`.
- Current CLI surface includes `exec`, `config`, `models`, `providers`, `doctor`, `search`, `sessions`, `plugins`, `hooks`, `mcp`, `sandbox`, `update`, `worktrees`, `verify`, `changes`, `serve`, `help`, and `version`.
- Core areas exist for agent flow, provider/model registry, tools, TUI, sessions, search, sandbox, self-verification, stream-json, usage, redaction, hooks, MCP, plugins, update, worktrees, and git/change handling.
- TypeScript is no longer the runtime. Remaining TypeScript should be treated as npm/package support, scripts, or legacy test support until each file is explicitly retired or rewritten.

Still needed before Zero feels production-ready:

- Permission and sandbox UX must become clearer and harder to misuse.
- TUI command flows need polish beyond basic rendering.
- Platform behavior, especially Windows sandbox/update/install paths, needs stronger product ownership.
- Review automation should produce useful, repeatable output for maintainers.
- Release trust needs checksums, install smoke tests, and update verification.

## 3. Ownership Rules

- Every feature has one DRI.
- UI, runtime, and platform work should be split by contract, not by vague file ownership.
- Every PR must expose user-runnable behavior or a concrete contract consumed by another owner.
- Docs, CI, and website work can support a product PR, but they should not be the whole lane for any owner.
- Anandan must own platform product work, not only docs/site/CI.
- Shared contracts should be agreed before large UI or integration work lands.
- Keep PRs large enough to move the product forward, but still reviewable.

## 4. Team Roles

| Owner | Primary Role | Owns | Should Not Own Alone |
| --- | --- | --- | --- |
| Vasanth | Product UX and TUI owner | Bubble Tea TUI, startup screen, command palette, slash command UX, tool rendering, permission prompts, sandbox violation rendering, verification/test/git output UX, themes, daily CLI feel | Provider internals, release pipeline, platform adapters |
| Gnanam | Runtime core owner | Provider/model registry, provider factory, agent/runtime protocols, stream-json, sessions/search backends, config/doctor/usage/redaction, MCP/hooks/plugins backend, sandbox policy/grants, permission event contract | TUI composition, release packaging, platform-specific install trust |
| Anandan | Platform product owner | Install/update/release trust, binary/package smoke tests, platform sandbox adapters, Windows behavior, PR/review automation CLI, CI as product verification, performance and release checks | TUI rendering, provider semantics, model registry ownership |

## 5. Anandan Platform Product Rule

Anandan should not be assigned only docs/site/CI forever. His lane must produce platform behavior that users and maintainers can run.

Accepted Anandan work:

- `zero sandbox policy` improvements and platform-specific sandbox behavior.
- Windows sandbox adapter behavior and clear fallback reporting.
- `zero update` verification, release metadata validation, and install smoke tests.
- Package and binary checksums.
- PR automation CLI workflows that reviewers can run locally or in CI.
- CI jobs that prove real Zero commands work after installation.

Not accepted as standalone Anandan work:

- Docs-only updates.
- Website-only updates.
- Future VS Code notes with no current CLI behavior.
- CI matrix changes that do not prove a user-facing command or release flow.
- Release notes or checklists without executable validation.

Docs and CI are still useful, but they must attach to real platform product behavior.

## 6. Shared Contracts

| Contract | DRI | Consumers | Purpose |
| --- | --- | --- | --- |
| Provider stream events | Gnanam | Vasanth, Anandan | Lets TUI and automation render model output, tool calls, and errors consistently. |
| Permission and sandbox events | Gnanam | Vasanth, Anandan | Lets TUI ask for decisions and platform code enforce them without bypasses. |
| Session event schema | Gnanam | Vasanth, Anandan | Lets TUI, resume, search, and automation share one session model. |
| Command metadata registry | Vasanth | Gnanam, Anandan | Lets slash commands, help, shell completions, and docs use one command source. |
| Platform/release contract | Anandan | Vasanth, Gnanam | Defines install, update, checksum, smoke, and platform capability results. |
| Stream-json protocol | Gnanam | Anandan | Lets CI and PR automation consume Zero output without scraping text UI. |

## 7. Active Workstreams

### A. Safe Autonomy

Top priority. Zero must be able to run tools confidently while showing users what is happening and asking when risk increases.

Planned PRs:

- Vasanth: `feat/tui-sandbox-permissions`
  - permission prompt UX
  - allow, deny, and always-allow flows
  - sandbox violation rendering
  - `/permissions` or equivalent permission state UI
- Gnanam: `feat/runtime-permission-events`
  - structured permission request events
  - sandbox grant contract
  - stream-json support
  - tests for denial, approval, and violation cases
- Anandan: `feat/windows-sandbox-platform`
  - platform sandbox capability checks
  - stronger Windows behavior or explicit safe fallback
  - `zero sandbox policy --json` output useful enough for CI and support
  - platform tests where possible

Done when:

- High-autonomy tool attempts produce a clear prompt, decision, and result in TUI and headless modes.
- Denied commands do not run.
- Sandbox violations are visible and actionable.
- Platform fallback is explicit, tested, and not silently unsafe.

### B. Command UX And Daily CLI

The CLI should feel like a product, not a collection of backend commands.

Planned PRs:

- Vasanth: `feat/tui-command-polish`
  - `/model`, `/provider`, `/context`, `/doctor`, `/verify`, `/resume`, and `/compact` become clear interactive flows
  - command results render consistently
  - startup screen stays minimal until the user asks for panels
- Gnanam: `feat/runtime-command-contracts`
  - stable command data APIs
  - provider/model/session/config result structs
  - error types suitable for TUI rendering
- Anandan: `feat/install-update-user-flow`
  - `zero update` and install flows are user-runnable
  - package smoke command verifies installed binary behavior
  - CI proves the packaged command starts and reports version/help

Done when:

- A new user can run Zero, inspect config/provider/model state, and understand the next action without reading source.

### C. Verification And Review Automation

Zero should help maintainers verify changes and review PRs with repeatable local commands.

Planned PRs:

- Vasanth: `feat/tui-verification-results`
  - render verify/test/git/review results in TUI
  - show pass/fail/blocked states clearly
- Gnanam: `feat/runtime-self-verify-contract`
  - structured verification results
  - stable test/change event model
  - redacted logs for model and automation use
- Anandan: `feat/pr-review-cli-workflow`
  - make `cmd/zero-pr-review` or `zero review` useful for changed files, validations, and summary output
  - support local and CI invocation
  - avoid brittle text scraping where stream-json can be used

Done when:

- A maintainer can run one local command before review and get useful changed-file, validation, and risk output.

### D. Release Trust And Distribution

This is Anandan's main product lane after platform sandbox work.

Planned PRs:

- Anandan: `feat/release-trust`
  - release metadata validation
  - checksums
  - install smoke tests
  - update verification
  - clear error output when release artifacts are invalid
- Vasanth:
  - consume release/update results in CLI UX where needed
- Gnanam:
  - ensure config, errors, and logs are structured and redacted

Done when:

- Users can install, run, verify, and update Zero with confidence.
- Maintainers can prove release artifacts work before publishing.

### E. Extensibility UX

This comes after safe autonomy and daily CLI polish.

Planned PRs:

- Vasanth:
  - `/mcp`, `/hooks`, `/plugins`, and related UI flows
- Gnanam:
  - harden backend contracts and lifecycle behavior
- Anandan:
  - automation and docs only after executable behavior exists

Done when:

- Extensibility feels discoverable and safe from the terminal.

## 8. Immediate Next PRs

These are the next reviewable slices. Do not use the old migration-era next steps.

1. Vasanth: `feat/tui-sandbox-permissions`
2. Gnanam: `feat/runtime-permission-events`
3. Anandan: `feat/windows-sandbox-platform`
4. Anandan: `feat/update-release-verification`
5. Vasanth: `feat/tui-command-polish`
6. Gnanam: `feat/session-rewind-compaction-contract`

Recommended first parallel set:

- Vasanth starts the TUI permission prompt and violation display.
- Gnanam defines the permission event/grant contract.
- Anandan owns Windows/platform sandbox reporting and tests.

## 9. Command Ownership Matrix

| Command Area | Vasanth | Gnanam | Anandan |
| --- | --- | --- | --- |
| `exec` | TUI and interactive UX | Runtime execution contract | Package smoke and platform validation |
| `sandbox` | Prompt and violation UX | Policy, grants, and events | Platform adapters and Windows behavior |
| `update` | Display and user messaging | Config and redacted errors | Primary owner of update verification |
| `verify` | Result rendering | Verification backend contract | CI and release integration |
| `changes` | TUI rendering | Change detection backend | PR automation integration |
| `mcp`, `hooks`, `plugins`, `serve` | Discoverability and UI | Backend lifecycle and contracts | Automation/docs after behavior exists |
| `sessions`, `search` | Resume/search UX | Storage, query, and event model | Stream-json and automation consumers |

## 10. Definition Of Done

For every code PR:

- Add or update tests for the changed behavior.
- Run `go test ./...` when Go files changed.
- Run the relevant build command when CLI entrypoints changed.
- Run package smoke checks when install/update/npm wrapper files changed.
- Include manual CLI output or screenshots only when they prove a user-facing flow.

For docs-only PRs:

- State clearly that no runtime behavior changed.
- Do not use docs-only PRs as a substitute for platform product work.

For Anandan's PRs specifically:

- Must include one of:
  - user-runnable command behavior
  - install/update/release artifact verification
  - platform adapter behavior
  - CI that executes real installed Zero commands
- Docs/CI can support the PR, but cannot be the entire value.

## 11. Review Rotation

- Vasanth PRs:
  - Gnanam reviews runtime contracts.
  - Anandan reviews platform/release impact when touched.
- Gnanam PRs:
  - Vasanth reviews user-facing contract impact.
  - Anandan reviews platform/protocol impact when touched.
- Anandan PRs:
  - Vasanth reviews user-facing CLI behavior.
  - Gnanam reviews runtime contract and safety impact.

## 12. Retired Historical Plan

The old M0-M6 TypeScript-to-Go migration schedule is now historical.

Do not assign new work from the old migration next-steps section. The Go migration foundation has already landed on main. Future work should move Zero from "ported" to "mature terminal coding agent."
