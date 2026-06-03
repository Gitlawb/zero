# zero

## Setup

```bash
bun install --frozen-lockfile
```

## Run

```bash
bun run dev
```

## Install From Release

Linux/macOS:

```bash
scripts/install.sh
```

Windows:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1
```

See `docs/INSTALL.md` for version, repository, and install path overrides.

## Checks

```bash
bun test
bun run typecheck
bun run build
bun run smoke:build
bun run perf:bench
bun run package:release
```

Check for released CLI updates:

```bash
./zero update --check
```

See `docs/PERFORMANCE.md` for benchmark thresholds and CI smoke behavior.

Bun version is pinned in `package.json`.
