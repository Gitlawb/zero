import { $ } from 'bun';
import { cp, mkdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import {
  getReleaseArchiveName,
  getReleasePackageName,
  zeroArtifactName,
  zeroArtifactPath,
} from './artifact';
import { parsePackageVersion } from './build';
import { writeSha256Checksum } from './release-checksums';

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
await mkdir(join(stagingDir, 'bin'), { recursive: true });
await mkdir(join(stagingDir, 'src'), { recursive: true });
await cp(join('bin', 'zero.ts'), join(stagingDir, 'bin', 'zero.ts'));
await cp(join('src', 'npm-wrapper.ts'), join(stagingDir, 'src', 'npm-wrapper.ts'));
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

const checksum = await writeSha256Checksum(archivePath);

console.log(`Packaged ${archiveName}`);
console.log(`Wrote ${checksum.archiveName}.sha256`);
