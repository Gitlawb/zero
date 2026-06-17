---
description: Project-level instruction set for the AI agent working in this repo. Ten discrete modes covering the recurring engineering workflows on Zero.
globs: "*.go, *.js, package.json, .github/workflows/*.yml, docs/*.md"
alwaysApply: false
---

# AGENTS.md

Instructions for the AI agent working inside the `zero` repository. Read this before any non-trivial change.

## 1. Repository map

### Entry points

- `cmd/zero` — primary CLI binary. Run with `go run ./cmd/zero`.
- `cmd/zero-release` — release tooling. `build`, `smoke`, `package`, `verify`. Never bypass.
- `cmd/zero-seccomp` — Linux seccomp profile generator for the sandbox.
- `cmd/zero-perf-bench` — performance smoke work.
- `cmd/zero-pr-review` — local PR-review runner.
- `bin/zero.js` — npm wrapper around the Go binary. Touch only when adjusting the npm surface.

### Core internal packages

- `agent/` — the agent loop. Prompt in, tool calls out, results back, repeat. `guardrails.go` holds the loop's stop and confirmation thresholds. `loop.go` is the main state machine.
- `tui/` — terminal UI built on `charm.land/bubbletea/v2` and `charm.land/lipgloss/v2`. `model.go` holds state, `view.go` is pure render, `command_center.go` dispatches slash commands, `theme.go` owns every color and border (hex literals are forbidden elsewhere).
- `providers/` — LLM adapters. One subdir per provider: `openai/`, `anthropic/`, `gemini/`, `providerio/`. `factory.go` registers them.
- `sandbox/` — exec gating and risk classification. `risk.go` defines patterns, `engine.go` decides allow/deny, `paths.go` and `pathlists.go` handle allow/deny lists.
- `sessions/` — session state, persistence, resume, title generation.
- `specialist/` — sub-agent routing. Specialists run scoped tasks with their own tool whitelist. `builtin.go` declares the interface, `runtime.go` runs them, `manifest.go` and `exec.go` are the execution paths.
- `swarm/` — multi-agent coordination for batched work.
- `mcp/` — Model Context Protocol client and tool bridge.
- `modelregistry/` — model catalog, capability flags (vision, tool calls, reasoning efforts), cost data. `catalog.go` is `DefaultModelEntries`.
- `providercatalog/` — provider descriptors (base URLs, auth env vars, default model).
- `tools/` — agent-callable tools. Each tool implements the `Tool` interface in `tools/types.go`. `registry.go` is the central map; `CoreToolsScoped` / `CoreReadOnlyToolsScoped` / `CoreWriteToolsScoped` / `CoreShellToolsScoped` / `CoreNetworkTools` are the registration sites.
- `cli/` — top-level command wiring. `app.go` is the entry, `command_center.go` dispatches subcommands, per-feature files (`exec.go`, `sessions.go`, `setup.go`, etc.) implement each subcommand.

Other packages worth knowing: `config/`, `redaction/`, `secrets/`, `imageinput/`, `cron/`, `doctor/`, `browser/`, `background/`, `lsp/`, `hooks/`, `update/`, `usage/`, `verify/`, `testrunner/`, `selfverify/`, `workspaceindex/`, `worktrees/`, `workspaceseed/`, `specmode/`, `streamjson/`, `notify/`, `keyring/`, `observability/`, `oauth/`, `provideroauth/`, `provideronboarding/`, `providerhealth/`, `providermodelcatalog/`, `providermodeldiscovery/`, `npmwrapper/`, `agenteval/`, `zerocommands/`, `zerogit/`, `zeroruntime/`, `release/`, `review/`, `repomap/`, `repoinfo/`, `search/`, `skills/`, `plugins/`.

### Test surface

- Tests live next to the file they cover (`foo.go` → `foo_test.go`). No separate `tests/` tree.
- Snapshot tests use `charmbracelet/x/exp/golden` (visible in `go.sum`).
- `go test ./internal/<pkg>/ -run TestX -v` for narrow runs; `go test ./...` for the full suite.
- No `-race` on Windows (no gcc toolchain). On Linux/macOS, `-race` is fine.

## 2. Conventions

- Go 1.25, toolchain `go1.26.4`. Charm libs are pinned to v2 (`charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`).
- Shell parsing uses `mvdan.cc/sh/v3`, not `os/exec` + string concatenation. Never invent your own shell tokenizer.
- `gofmt -l <files>` and `go vet ./...` must pass before commit. CI enforces both.
- The npm wrapper at `bin/zero.js` is the only Node-touching surface; everything else is Go-native. Do not add Bun or TypeScript build deps.
- Theme tokens are the single source of truth for color and border. Hex literals anywhere except `internal/tui/theme.go` are rejected.
- The TUI split is strict: `model.go` owns state, `view.go` is pure render. Styling calls (`Style.Render`, `NewStyle`) live in `view.go` and render helpers under `tui/`; `model.go` may call `lipgloss.Width` for column measurements but never applies styles.
- Errors are wrapped with `fmt.Errorf("...: %w", err)` and surface context. Never swallow errors silently.
- Public API changes require a one-line note in the PR body explaining the impact and the migration path.

## 3. Ten agent modes

Each mode is a discrete workflow the AI agent adopts when the task matches. Pick the mode at the start of the task; do not blend them.

### Mode 1 — Add a slash command

Touch files:

- `internal/tui/commands.go` — add a `commandDefinition` entry. Set `name`, `aliases`, `usage`, `group`, `description`, `kind` (one of the `commandKind` constants in this file).
- `internal/tui/command_center.go` — add a `case command<Name>:` branch to the slash-command dispatch. Match the dispatch shape used by existing commands: read `command.text`, mutate model state, append a transcript row.
- `internal/tui/<feature>.go` — co-locate the handler with the related feature. Handler signature is `(model, args) (model, string)` where the string is the status text the transcript displays.
- `internal/tui/view.go` or `internal/tui/command_output.go` — only if the command renders a card or panel; reuse the existing `commandCard` and `commandOutput` helpers.

Pattern to follow: copy `commandEffort` end-to-end (definition in `commands.go`, dispatch in `command_center.go`, handler in `session_controls.go`). Do not invent a new shape.

What NOT to do:

- Do not register the command in two places. One definition, one dispatch, one handler.
- Do not read input from `m.input.Value()` inside the handler. The dispatch already passes `command.text`.
- Do not mutate `m.picker` from inside a slash-command handler. Use `m.openXxxPicker()` if a picker is needed.

Verification:

```
go build ./cmd/zero
go test ./internal/tui/ -run Test<Command> -v
```

### Mode 2 — Add a tool

Touch files:

- `internal/tools/<name>.go` — implement the `Tool` interface from `internal/tools/types.go`. Required methods: `Name()`, `Description()`, `Parameters()`, `Safety()`, `Run(ctx, args) Result`. Fill `Safety{ SideEffect, Permission, Reason }` honestly.
- `internal/tools/registry.go` — register the tool by adding it to the appropriate `CoreToolsScoped` variant (read-only, write, shell, network, or full). The factory functions decide which tool set a context gets.
- `internal/tools/<name>_test.go` — table-driven test covering the happy path, each error path declared in the schema, and one permission-boundary case.

Pattern to follow: copy `internal/tools/read_file.go` end-to-end. It demonstrates the `Tool` interface, the schema pattern, and the test layout.

What NOT to do:

- Do not call `os/exec` directly. The bash tool is the only path; everything else routes through `mvdan.cc/sh/v3` indirectly or uses Go stdlib.
- Do not mutate `m.transcript` from inside a tool. Tools return `Result`; the agent loop appends to the transcript.
- Do not skip `Safety`. Every tool must declare `SideEffect` and `Permission`.
- Do not add new top-level deps. If a tool needs a library, justify it in the PR body.

Verification:

```
go test ./internal/tools/ -run Test<Name> -v
go vet ./internal/tools/
```

### Mode 3 — Add a provider

Touch files:

- `internal/providers/<name>/` — new subdir. Mirror `internal/providers/openai/` (factory wiring, request types, response mapping, image / tool-call handling, tests).
- `internal/providers/factory.go` — register the new provider in the factory switch.
- `internal/providercatalog/catalog.go` — add a `Descriptor` entry (ID, display name, default base URL, auth env vars, default model, capabilities).
- `internal/cli/...` — only if the provider needs a new flag. Mirror existing flags before adding new ones.

Pattern to follow: copy `internal/providers/openai/`. It has factory wiring, request shaping, content-part conversion, image blocks, and tests for every code path.

What NOT to do:

- Do not skip the image / vision path. Even if your first model doesn't support vision, the provider must handle `zeroruntime.ImageBlock` correctly (Anthropic `image` block, OpenAI `image_url`, Gemini `inlineData`).
- Do not invent your own HTTP client. Reuse the one in `internal/providers/openai/` or `internal/providerio/`.
- Do not hardcode model IDs. They live in `internal/modelregistry/`.

Verification:

```
go test ./internal/providers/<name>/ -v
go test ./internal/providers/ -v
go test ./internal/providercatalog/ -v
```

### Mode 4 — Add a model entry

Touch files:

- `internal/modelregistry/catalog.go` — extend `DefaultModelEntries()` with the new model. Fields: ID, Provider, DisplayName, ContextWindow, Capabilities (`vision`, `tool_call`, `reasoning`), ReasoningEfforts (if applicable), Cost.

Pattern to follow: open `catalog.go`, find the existing entry for the same provider, copy the row, edit the values. Same shape, same fields.

What NOT to do:

- Do not invent capabilities. Set only the ones the model actually supports. If unsure, leave the capability unset.
- Do not add cost data you cannot cite. If you don't have a source, leave the cost fields zero and note it in the PR.

Verification:

```
go test ./internal/modelregistry/ -v
go test ./internal/tui/ -run TestModel -v
```

### Mode 5 — Add a specialist

Touch files:

- `internal/specialist/<name>.go` — implement the specialist. Read `internal/specialist/builtin.go` for the interface and `runtime.go` for the execution model.
- `internal/specialist/registry.go` — register the specialist. Update the routing table so the main agent knows when to dispatch to it.

Pattern to follow: copy `internal/specialist/exec.go` (or another existing specialist). The shape is consistent.

What NOT to do:

- Do not give a specialist access to tools the main agent doesn't. Specialists have their own whitelist.
- Do not let a specialist call another specialist. One level of dispatch only.
- Do not register a specialist without a routing rule. Routing without a rule is unreachable code.

Verification:

```
go test ./internal/specialist/ -v
go test ./internal/agent/ -v
```

### Mode 6 — Add a sandbox rule

Touch files:

- `internal/sandbox/risk.go` — add a pattern to the appropriate risk bucket (`destructive`, `destructive_catastrophic`, `network`, `credential`, etc.).
- `internal/sandbox/engine.go` — only if the rule needs a new decision branch.
- `internal/sandbox/risk_hardening_test.go` (or a sibling test file) — write a failing test FIRST that exercises the new pattern.

Pattern to follow: read `risk.go` to see the existing pattern classification. The `add()` helper takes `(pattern, RiskLevel)`. New rules sit alongside their category.

What NOT to do:

- Do not weaken an existing rule. If you find a false positive, write a more specific pattern that excludes the safe case rather than removing the dangerous one.
- Do not add a rule without a test. Sandbox regressions are silent and dangerous.
- Do not skip `requestPaths` checks. When a command takes paths, the sandbox must validate them against the workspace + grant scope.

Verification:

```
go test ./internal/sandbox/ -v
go test ./internal/sandbox/ -run TestRisk -v
```

### Mode 7 — Modify the TUI

Touch files:

- `internal/tui/model.go` — state only. Add fields, add cases to `Update()`, add dispatch logic. Never apply lipgloss styles here.
- `internal/tui/view.go` — pure render. Read from `model`, render to styled strings. Every style application lives here or in a `render*` helper.
- `internal/tui/<feature>.go` — split when the feature is large. Look at the existing per-feature files (`picker.go`, `session.go`, `transcript_body.go`, etc.) for the convention.
- `internal/tui/theme.go` — only if you need a new color or border. Add a named token with a hex value; never inline a hex elsewhere.

Pattern to follow: open `model.go` and `view.go` side-by-side. Find the feature you're extending. The state mutation lives in `model.go`; the renderer lives in `view.go`. Tests live in `*_test.go` next to each.

What NOT to do:

- Do not apply lipgloss styles from `model.go`.
- Do not store styled strings in the model. Store raw values; render at draw time.
- Do not bypass `m.transcript`. Every visible row goes through `appendTranscriptRow`.
- Do not add a hex color literal anywhere except `theme.go`.

Verification:

```
gofmt -l internal/tui/
go vet ./internal/tui/
go test ./internal/tui/ -count=1
```

### Mode 8 — Debug a failing test

Steps:

1. Run narrow: `go test ./internal/<pkg>/ -run Test<Name> -v -count=1`. Read the failure verbatim — the assertion message is the most reliable signal.
2. Read the test. The test is the contract. Do not rewrite the test to match broken behavior.
3. Read the production code under test. Find the divergence between what the test expects and what the code does.
4. Fix the production code. If the production code is right and the test was wrong, fix the test — but only after proving the production behavior is correct.
5. Run narrow again. Then run the package. Then run the adjacent packages. Then run `go test ./...` to catch regressions.

What NOT to do:

- Do not `t.Skip()` to make a test pass. Skip with a TODO comment and a linked issue at most.
- Do not delete a failing test without understanding why it was added.
- Do not change a test's expected value without a commit message explaining why.
- Do not modify `time.Now()` or any clock source to make a timing-sensitive test pass.

Verification:

```
go test ./internal/<pkg>/ -run Test<Name> -v -count=1
go test ./internal/<pkg>/ -count=1
go test ./...
```

### Mode 9 — Cut a release

Touch files:

- `cmd/zero-release` only. No manual `git tag`. No manual binary upload. No edits to `CHANGELOG.md` by hand.

Pattern to follow: run `go run ./cmd/zero-release build`, then `go run ./cmd/zero-release smoke`, then `go run ./cmd/zero-release package`, then `go run ./cmd/zero-release verify`. The release tooling is the contract.

What NOT to do:

- Do not push a tag by hand. The release tooling owns tag creation.
- Do not skip `smoke`. If smoke fails, the release fails.
- Do not edit `CHANGELOG.md` manually. The release tooling generates it from `git log` since the previous tag.
- Do not bypass `verify`. Verification is the last gate.

Verification:

```
go run ./cmd/zero-release build
go run ./cmd/zero-release smoke
go run ./cmd/zero-release package
go run ./cmd/zero-release verify
```

### Mode 10 — Run a security or quality audit

Touch files (read-only by default; edits only after explicit user approval):

- `internal/sandbox/` — destructive-command classification in `risk.go`, decision logic in `engine.go`, path allow/deny in `paths.go` and `pathlists.go`. Audit coverage: do destructive paths actually get caught? Are grant scopes respected? Are `..` traversals blocked?
- `internal/secrets/` — env handling. Audit: are secrets redacted in logs? Are `os.Getenv` calls going through the keyring when configured?
- `internal/redaction/` — output redaction. Audit: is the redaction list current? Are there new tool outputs that need redaction entries?
- `internal/providers/` — provider auth. Audit: are API keys read from env, never persisted? Are provider adapters validating their inputs?
- `internal/mcp/` — MCP server trust. Audit: are MCP server outputs treated as data, never as instructions? Is the schema validated?
- `internal/hooks/` — pre/post command hooks. Audit: do hooks have a way to abort? Is the abort path tested?
- `docs/audit/` — read the most recent `*-deep-audit*.md` and `*-reverification.md` to see what's already been reviewed. Do not re-audit a category already covered.

Pattern to follow: pick one subsystem per audit pass. Read the code, run the existing tests, write a one-page report naming each finding with a `file:line` anchor and an exploitability classification (`exploitable | plausible | theoretical`).

What NOT to do:

- Do not audit and edit in the same pass. Audit first, get user sign-off, then fix.
- Do not propose fixes that disable a security control to silence a false positive. Tighten the rule instead.
- Do not skip the audit because a recent audit covered the area. Confirm the diff since the last audit is empty.
- Do not write findings without a `file:line` anchor. Vague findings get deleted.

Verification:

```
go test ./internal/sandbox/ -v
go test ./internal/secrets/ -v
go test ./internal/redaction/ -v
go test ./internal/mcp/ -v
```

## 4. Hard rules across all modes

- Never run `rm -rf`, `git reset --hard`, or force-push without an explicit user message.
- Never edit a file you have not read in full first.
- Never widen `Permission` from `prompt` to `allow` for a tool without a test that proves the wider permission is safe.
- Never delete a test. The test is a contract.
- Never bypass `go vet` or `gofmt` with a `// nolint:` comment unless the PR body explains why.
- Never commit secrets, even temporarily. If a secret lands in a commit, revert the commit and rotate the secret.
