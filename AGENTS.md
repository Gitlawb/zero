---
description: Project guidelines for the open-source CLI coding agent (zero) repository.
globs: "*.go, *.js, *.md, *.json, *.toml, *.yaml, *.yml"
alwaysApply: false
---

# Repository Conventions for Zero

This file outlines the project conventions and repository guidelines for coding agents when working on the `zero` repository. For the general guide on how to extend Zero (write specialist sub-agents, hooks, plugins, MCP, skills), see [docs/EXTENDING.md](docs/EXTENDING.md).

## 1. Project Conventions

- Build with `make`, not `go build` directly.
- Tests live next to the source file (`foo_test.go` next to `foo.go`).
- Run `make lint` before opening a PR.
- Never edit files under `third_party/` — those are vendored.

## 2. Guidelines for Coding Agents

If you are an AI coding agent executing tasks in this repository, you **MUST** run all Go code quality and security checks before committing code or completing your task:

1. **Format & Vet**: Run `go fmt ./...` and `go vet ./...`.
2. **Lint**: Run `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run --enable-only unused,ineffassign,staticcheck ./...`.
3. **Security**: Run `go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...`.

If any check fails or cannot be run, do not ignore it. Prompt the user for instructions or setup assistance before proceeding.
