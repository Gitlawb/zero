# npm Wrapper Smoke Checklist

Run this checklist when a PR changes npm distribution files such as
`package.json`, `bun.lock`, `index.ts`, build scripts, package release scripts,
or the npm `bin` wrapper.

## Required Checks

```bash
bun install --frozen-lockfile
bun run build
bun run smoke:build
```

Also run the Go checks when the PR changes Go entrypoint, CLI, or release
artifact behavior:

```bash
bun run test:go
bun run build:go
bun run smoke:go
```

## Checklist

- `package.json` has the expected package name, version, and `bin.zero` entry.
- `bun install --frozen-lockfile` succeeds without lockfile changes.
- The wrapper binary resolves through the package `bin` entry and
  `node_modules/.bin` in a package-install smoke test.
- The built wrapper exits 0 for `zero --version` or `zero --help`.
- The reported version matches `package.json`.
- Release packaging still emits the expected archive and checksum names when
  package release files change.
