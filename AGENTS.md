# Repository Guidelines for Zero

These instructions apply to all work in this repository. For the user-facing
guide to extending Zero with specialists, hooks, plugins, MCP, and skills, see
[docs/EXTENDING.md](docs/EXTENDING.md).

## 1. Contribution and Pull Request Rules

- Before opening any pull request, **all contributors**—including maintainers,
  community contributors, and coding agents—must read and follow
  [CONTRIBUTING.md](CONTRIBUTING.md).
- Community pull requests require an existing parent issue with the
  `issue-approved` label. Team members may open pull requests through the
  internal development process described in `CONTRIBUTING.md`.
- Keep each change focused on the approved or assigned scope. Do not include
  unrelated fixes, refactors, formatting churn, generated output, or existing
  local changes in the same commit or pull request.
- Discuss new implementation languages, runtimes, major dependency changes,
  and broad architectural rewrites with maintainers before implementation.
- Pull request descriptions must explain what changed and why, link the parent
  issue when required, and list the tests or verification performed. Include
  screenshots or a short recording for user-visible UI changes when practical.

## 2. Repository and Implementation Conventions

- Use the Go version declared in `go.mod`. Do not hardcode a different local
  toolchain version in scripts or documentation.
- Use the repository build and release commands (`make` and
  `go run ./cmd/zero-release ...`) instead of inventing parallel build flows.
- Keep tests beside their source files (`foo_test.go` next to `foo.go`). Add a
  regression test for behavior changes and run affected concurrent code under
  the race detector.
- Never edit files under `third_party/`; they are vendored.
- Prefer one cross-platform function with small conditional checks over
  duplicated platform-specific helpers when the behavior can remain unified.
- Do not commit generated benchmark reports from
  `internal/perfbench/reports/*.json`; reports are configuration-specific
  evidence, not repository state.
- Preserve the user's working tree. Do not overwrite, delete, stage, or commit
  unrelated tracked or untracked files.

## 3. Required Validation

Run validation from the repository root before committing, opening a pull
request, or completing an implementation task:

1. **Formatting check**: `make fmt-check`. If it fails, format with
   `go fmt ./...` (or `make fmt`) and run the check again.
2. **Vet**: `go vet ./...` (or `make vet`).
3. **Tests**: `go test ./...`. Use `make test` for the full race-enabled suite,
   or run focused tests with `-race`, when concurrency is affected.
4. **Build**: `go run ./cmd/zero-release build`.
5. **Smoke test**: `go run ./cmd/zero-release smoke`.
6. **Advisory lint**:
   `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run --enable-only unused,ineffassign,staticcheck ./...`.
7. **Security**:
   `go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...`.
8. **Diff hygiene**: `git diff HEAD --check` (covers staged and unstaged
   tracked changes).

`make lint` currently runs the formatting check and `go vet`; it does **not**
run golangci-lint. The pinned golangci-lint job is advisory in CI while the
existing repository-wide backlog is cleaned up. Fix findings introduced by or
related to the current change. Unrelated pre-existing advisory findings do not
justify expanding a focused pull request; report them separately.

Formatting, vet, tests, build, smoke, diff hygiene, and govulncheck are hard
requirements. If a related check fails, fix it. If a required check cannot run
because of the environment or fails for an unrelated external reason, report
the exact failure and obtain maintainer direction rather than silently ignoring
it.
