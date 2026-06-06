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
