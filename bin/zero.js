#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { createRequire } from 'node:module';
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

// Legacy path: helpers installed as npm dependencies of the wrapper. Current
// installs vendor the helpers inside the platform package instead (a helpers/
// directory next to the binary, found by the Go side on its own), so this
// manifest is usually empty — it only matters for a downloaded-binary install
// whose wrapper still carries helper dependencies.
function localControlHelperManifest(packageRoot) {
  const helpers = {};
  for (const name of ['agent-browser', 'tuistory']) {
    const helper = resolveHelper(packageRoot, name);
    if (helper) helpers[name] = helper;
  }
  if (Object.keys(helpers).length === 0) return '';
  return JSON.stringify({ version: 1, helpers });
}

// The platform payload is a version of @gitlawb/zero itself, installed under
// an npm: alias (see docs/NPM_PACKAGING.md). The alias name is derived from
// process.platform/process.arch, so an unsupported platform simply fails to
// resolve and we fall through to the downloader.
function platformPackageBinary() {
  const aliasedName = `@gitlawb/zero-${process.platform}-${process.arch}`;
  let manifestPath;
  try {
    manifestPath = createRequire(import.meta.url).resolve(`${aliasedName}/package.json`);
  } catch {
    return null;
  }
  const candidate = join(dirname(manifestPath), zeroBinaryName());
  return existsSync(candidate) ? candidate : null;
}

// Mirrors the release matrix (internal/release/release.go): the platforms a
// GitHub Release asset exists for. android maps to the linux asset; there is
// no windows-arm64 asset (Windows on ARM runs the x64 build under emulation).
function releaseAssetAvailable() {
  const platform = { linux: 'linux', android: 'linux', darwin: 'macos', win32: 'windows' }[
    process.platform
  ];
  const arch = process.arch === 'x64' || process.arch === 'arm64' ? process.arch : null;
  if (!platform || !arch) return false;
  return !(platform === 'windows' && arch === 'arm64');
}

// Fallback for installs without a platform package (--omit=optional, package
// managers that skip optionalDependencies): fetch the binary next to the
// wrapper with the same checksum-verified downloader that used to run as
// postinstall. Deliberately NOT cached on failure — a transient network error
// self-heals on the next run — so this retries on every invocation until a
// binary is in place. Platforms with no release asset skip the attempt.
function downloadMissingBinary(packageRoot) {
  const downloadScript = join(packageRoot, 'scripts', 'postinstall.mjs');
  if (!existsSync(downloadScript) || !releaseAssetAvailable()) return;
  console.error(
    '[zero] no platform package installed — fetching the native binary from the GitHub Release (retried on each run until it succeeds).',
  );
  spawnSync(process.execPath, [downloadScript], {
    stdio: ['ignore', 'inherit', 'inherit'],
  });
}

const packageRoot = dirname(dirname(fileURLToPath(import.meta.url)));

function resolveNativeBinary() {
  const fromPlatformPackage = platformPackageBinary();
  if (fromPlatformPackage) return fromPlatformPackage;
  const downloaded = join(packageRoot, zeroBinaryName());
  if (existsSync(downloaded)) return downloaded;
  downloadMissingBinary(packageRoot);
  return existsSync(downloaded) ? downloaded : null;
}

const nativePath = resolveNativeBinary();

if (!nativePath) {
  console.error(
    '[zero] No native binary is available for this install.\n' +
      'Normally npm installs it as an optional dependency of @gitlawb/zero\n' +
      `(@gitlawb/zero-${process.platform}-${process.arch}), and the wrapper can\n` +
      'also download it from the GitHub Release when it is missing.\n' +
      '\n' +
      'Things to try:\n' +
      '  - reinstall without omitting optional dependencies:\n' +
      '      npm install -g @gitlawb/zero\n' +
      '  - run the downloader manually (needs write access to the package dir):\n' +
      `      node "${join(packageRoot, 'scripts', 'postinstall.mjs')}"\n` +
      '\n' +
      'If this platform has no prebuilt binary, build from source:\n' +
      'https://github.com/Gitlawb/zero (go run ./cmd/zero, requires Go 1.26+).',
  );
  process.exit(1);
}

const env = { ...process.env };
const localControlHelpers = localControlHelperManifest(packageRoot);
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
