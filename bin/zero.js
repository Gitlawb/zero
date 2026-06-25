#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

function zeroBinaryName(platform = process.platform) {
  return platform === 'win32' ? 'zero.exe' : 'zero';
}

function helperShimNames(name, platform = process.platform) {
  if (platform === 'win32') {
    return [`${name}.cmd`, `${name}.exe`, name];
  }
  return [name];
}

function commandForShim(path, platform = process.platform) {
  if (platform === 'win32' && path.toLowerCase().endsWith('.cmd')) {
    return {
      command: process.env.ComSpec || 'cmd.exe',
      prefixArgs: ['/d', '/s', '/c', `"${path.replace(/"/g, '""')}"`],
    };
  }
  return { command: path, prefixArgs: [] };
}

function resolveHelper(packageRoot, name) {
  const binDir = join(packageRoot, 'node_modules', '.bin');
  for (const shimName of helperShimNames(name)) {
    const candidate = join(binDir, shimName);
    if (!existsSync(candidate)) continue;
    return {
      ...commandForShim(candidate),
      pathPrepend: [binDir],
    };
  }
  return null;
}

function localControlHelperManifest(packageRoot) {
  const helpers = {};
  for (const name of ['agent-browser', 'tuistory']) {
    const helper = resolveHelper(packageRoot, name);
    if (helper) helpers[name] = helper;
  }
  if (Object.keys(helpers).length === 0) return '';
  return JSON.stringify({ version: 1, helpers });
}

const packageRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const nativePath = join(packageRoot, zeroBinaryName());
const localControlHelpers = localControlHelperManifest(packageRoot);

if (!existsSync(nativePath)) {
  console.error(
    '[zero] No native binary found next to the npm wrapper. Reinstall the zero package or run `go run ./cmd/zero-release build` from the repository.'
  );
  process.exit(1);
}

const env = { ...process.env };
if (localControlHelpers) {
  env.ZERO_LOCAL_CONTROL_HELPERS = localControlHelpers;
} else {
  delete env.ZERO_LOCAL_CONTROL_HELPERS;
}

const child = spawnSync(nativePath, process.argv.slice(2), {
  stdio: 'inherit',
  env,
});

if (child.error) {
  console.error(`[zero] Failed to launch wrapper target: ${child.error.message}`);
  process.exit(1);
}

if (child.signal) {
  process.kill(process.pid, child.signal);
}

process.exit(child.status ?? 1);
