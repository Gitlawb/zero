# zero

## Setup

```bash
bun install --frozen-lockfile
```

## Run

```bash
bun run dev
```

## Checks

```bash
bun test
bun run typecheck
bun run build
bun run smoke:build
bun run package:release
```

Check for released CLI updates:

```bash
./zero update --check
```

Bun version is pinned in `package.json`.
