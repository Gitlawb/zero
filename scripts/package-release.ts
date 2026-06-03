import { $ } from 'bun';
import { createHash } from 'node:crypto';
import { cp, mkdir, rm, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import {
  getReleaseArchiveName,
  getReleasePackageName,
  zeroArtifactName,
  zeroArtifactPath,
} from './artifact';

function parsePackageVersion(packageText: string): string {
  const parsed = JSON.parse(packageText) as { version?: unknown };

  if (typeof parsed.version !== 'string' || parsed.version.trim() === '') {
    throw new Error('package.json must contain a non-empty string version');
  }

  return parsed.version;
}

function quotePowerShellPath(path: string): string {
  return `'${path.replaceAll("'", "''")}'`;
}

async function run(command: string[]): Promise<void> {
  const child = Bun.spawn(command, {
    stderr: 'pipe',
    stdout: 'pipe',
  });

  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);

  if (stdout.trim()) console.log(stdout.trim());

  if (exitCode !== 0) {
    const message = stderr.trim() || `${command[0]} exited with ${exitCode}`;
    throw new Error(message);
  }
}

async function sha256(path: string): Promise<string> {
  const bytes = await Bun.file(path).arrayBuffer();
  return createHash('sha256').update(Buffer.from(bytes)).digest('hex');
}

const packageText = await Bun.file('package.json').text();
const version = parsePackageVersion(packageText);
const packageName = getReleasePackageName(version);
const archiveName = getReleaseArchiveName(version);
const releaseDir = join(process.cwd(), 'dist', 'release');
const stagingRoot = join(process.cwd(), 'dist', 'package');
const stagingDir = join(stagingRoot, packageName);
const archivePath = join(releaseDir, archiveName);
const stagedBinaryPath = join(stagingDir, zeroArtifactName);

await rm(releaseDir, { recursive: true, force: true });
await rm(stagingRoot, { recursive: true, force: true });
await mkdir(stagingDir, { recursive: true });
await mkdir(releaseDir, { recursive: true });

await $`bun run build`;
await $`bun run smoke:build`;
await cp(zeroArtifactPath, stagedBinaryPath);
await cp('README.md', join(stagingDir, 'README.md'));
await cp('package.json', join(stagingDir, 'package.json'));
await writeFile(join(stagingDir, 'VERSION'), `${version}\n`);

if (process.platform === 'win32') {
  const sourceGlob = join(stagingDir, '*');
  const command = [
    'powershell',
    '-NoProfile',
    '-NonInteractive',
    '-ExecutionPolicy',
    'Bypass',
    '-Command',
    `Compress-Archive -Path ${quotePowerShellPath(sourceGlob)} -DestinationPath ${quotePowerShellPath(archivePath)} -Force`,
  ];
  await run(command);
} else {
  await $`chmod 755 ${stagedBinaryPath}`;
  await $`tar -C ${stagingDir} -czf ${archivePath} .`;
}

const checksum = await sha256(archivePath);
await writeFile(`${archivePath}.sha256`, `${checksum}  ${basename(archivePath)}\n`);

console.log(`Packaged ${archiveName}`);
console.log(`Wrote ${archiveName}.sha256`);
