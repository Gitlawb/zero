---
description: Use Go-native project commands; Node is only for the npm wrapper.
globs: "*.go, *.js, package.json, .github/workflows/*.yml, docs/*.md"
alwaysApply: false
---

Default to Go commands in this repository.

- Use `go run ./cmd/zero` to launch the local CLI.
- Use `go test ./...` for the repository test suite.
- Use `go run ./cmd/zero-release build` to build the release-facing binary.
- Use `go run ./cmd/zero-release smoke` to smoke-test the built binary.
- Use `go run ./cmd/zero-release package` and `go run ./cmd/zero-release verify` for release artifact checks.
- Use `go run ./cmd/zero-perf-bench` for performance smoke work.
- Do not add Bun or TypeScript build/test dependencies for repository validation.
- Use Node only when directly exercising the npm wrapper at `bin/zero.js`.

Source context (per design doc): Primary Go TUI sources (internal/tui/*, internal/zeroline/*, internal/cli/* incl. app.go/zeroline.go, cmd/zero/main.go, go.mod, docs/*.md, AGENTS.MD) live in task-specific worktrees under ~/.local/state/zero/worktrees/... (this checked-out tree). ustom/ prototypes read from /home/anaxy/Projects/. Separate TS/Ink surface (e.g. Projects/zero-featuress/zero) is NOT the Go target — designs/grounding are for pure Go Bubble Tea + Lip Gloss only (no Ink per AGENTS.MD + go.mod). All citations use relative module-root paths (e.g. "internal/cli/app.go"). Go-native validation only: `go test ./...`, `go run ./cmd/zero`, `go run ./cmd/zero-release ...` (Node only for wrapper).

Hybrid is the shipped default target (V1 home + V4 timeline execution per PR6); pure transcriptView deprecated for main path; 5 variations kept as refs + hybrid spec; review flows via /spec (not top-level /review); review verdict surface qualified as skeleton/partial (spec_mode.go + internal/review). See design doc Hybrid Target + PR Plan + References. Relative paths only.
