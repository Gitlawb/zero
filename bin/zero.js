#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

function zeroBinaryName(platform = process.platform) {
  return platform === 'win32' ? 'zero.exe' : 'zero';
}

const packageRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const nativePath = join(packageRoot, zeroBinaryName());

if (!existsSync(nativePath)) {
  console.error(
    '[zero] No native binary found next to the npm wrapper. Reinstall the zero package or run `go run ./cmd/zero-release build` from the repository.'
  );
  process.exit(1);
}

const child = spawnSync(nativePath, process.argv.slice(2), {
  stdio: 'inherit',
});

if (child.error) {
  console.error(`[zero] Failed to launch wrapper target: ${child.error.message}`);
  process.exit(1);
}

if (child.signal) {
  process.kill(process.pid, child.signal);
}

process.exit(child.status ?? 1);
