# npm Wrapper Smoke Checklist

Run this checklist when a PR changes npm distribution files such as
`package.json`, `bin/zero.js`, Go build or release tooling, or the npm `bin`
wrapper.

## Required Checks

```bash
go test ./internal/npmwrapper ./internal/release
go run ./cmd/zero-release build
go run ./cmd/zero-release smoke
```

Also run the Go checks when the PR changes Go entrypoint, CLI, or release
artifact behavior:

```bash
go test ./...
go run ./cmd/zero-release build
go run ./cmd/zero-release smoke
```

## Checklist

- `package.json` has the expected package name, version, and `bin.zero` entry.
- The wrapper binary resolves through the package `bin` entry and
  `node_modules/.bin` in a package-install smoke test.
- The built binary exits 0 for `zero --version` or `zero --help`.
- `zero --version` reports `zero <package.json version>`.
- Release packaging still emits the expected archive and checksum names when
  package release files change.
