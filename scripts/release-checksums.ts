import { createHash } from 'node:crypto';
import { readdir, writeFile } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';

export interface ParsedSha256Checksum {
  checksum: string;
  fileName: string;
}

export interface WrittenReleaseChecksum {
  archivePath: string;
  checksumPath: string;
  archiveName: string;
  checksum: string;
}

export interface VerifiedReleaseChecksum extends WrittenReleaseChecksum {
  expectedChecksum: string;
  actualChecksum: string;
}

export interface VerifyReleaseChecksumsOptions {
  releaseDir?: string;
}

export async function sha256File(path: string): Promise<string> {
  const bytes = await Bun.file(path).arrayBuffer();
  return createHash('sha256').update(Buffer.from(bytes)).digest('hex');
}

export function parseSha256Checksum(text: string): ParsedSha256Checksum {
  const lines = text.split(/\r?\n/).map((value) => value.trim()).filter(Boolean);

  if (lines.length === 0) {
    throw new Error('Checksum file is empty');
  }

  if (lines.length > 1) {
    throw new Error('Checksum file must contain exactly one checksum line');
  }

  const line = lines[0]!;
  const match = /^([a-fA-F0-9]{64})\s+(.+)$/.exec(line);
  if (!match) {
    throw new Error('Checksum file must contain "<sha256>  <archive-name>"');
  }

  const checksum = match[1]!.toLowerCase();
  const fileName = match[2]!.trim();
  assertSafeChecksumFileName(fileName);

  return { checksum, fileName };
}

export function formatSha256Checksum(checksum: string, fileName: string): string {
  if (!/^[a-fA-F0-9]{64}$/.test(checksum)) {
    throw new Error('SHA-256 checksum must be 64 hexadecimal characters');
  }

  assertSafeChecksumFileName(fileName);
  return `${checksum.toLowerCase()}  ${fileName}\n`;
}

export async function writeSha256Checksum(archivePath: string): Promise<WrittenReleaseChecksum> {
  const archiveName = basename(archivePath);
  const checksum = await sha256File(archivePath);
  const checksumPath = `${archivePath}.sha256`;

  await writeFile(checksumPath, formatSha256Checksum(checksum, archiveName), 'utf-8');

  return {
    archivePath,
    checksumPath,
    archiveName,
    checksum,
  };
}

export async function verifySha256Checksum(
  checksumPath: string
): Promise<VerifiedReleaseChecksum> {
  const checksumText = await Bun.file(checksumPath).text();
  const parsed = parseSha256Checksum(checksumText);
  const archivePath = join(dirname(checksumPath), parsed.fileName);

  if (!(await Bun.file(archivePath).exists())) {
    throw new Error(`Archive referenced by checksum does not exist: ${parsed.fileName}`);
  }

  const actualChecksum = await sha256File(archivePath);
  if (actualChecksum !== parsed.checksum) {
    throw new Error(
      `Checksum mismatch for ${parsed.fileName}: expected ${parsed.checksum}, got ${actualChecksum}`
    );
  }

  return {
    archivePath,
    checksumPath,
    archiveName: parsed.fileName,
    checksum: parsed.checksum,
    expectedChecksum: parsed.checksum,
    actualChecksum,
  };
}

export async function verifyReleaseChecksums(
  options: VerifyReleaseChecksumsOptions = {}
): Promise<VerifiedReleaseChecksum[]> {
  const releaseDir = options.releaseDir ?? join(process.cwd(), 'dist', 'release');
  const entries = await readdir(releaseDir, { withFileTypes: true });
  const files = entries.filter((entry) => entry.isFile()).map((entry) => entry.name).sort();
  const archiveNames = files.filter((name) => !name.endsWith('.sha256'));
  const checksumNames = files.filter((name) => name.endsWith('.sha256'));

  if (archiveNames.length === 0) {
    throw new Error(`No release archives found in ${releaseDir}`);
  }

  const expectedChecksumNames = new Set(archiveNames.map((name) => `${name}.sha256`));
  for (const checksumName of checksumNames) {
    if (!expectedChecksumNames.has(checksumName)) {
      throw new Error(`Unexpected checksum file without matching archive: ${checksumName}`);
    }
  }

  const verified: VerifiedReleaseChecksum[] = [];
  for (const archiveName of archiveNames) {
    const checksumName = `${archiveName}.sha256`;
    if (!checksumNames.includes(checksumName)) {
      throw new Error(`Missing checksum file: ${checksumName}`);
    }

    const result = await verifySha256Checksum(join(releaseDir, checksumName));
    if (result.archiveName !== archiveName) {
      throw new Error(
        `Checksum file ${checksumName} references ${result.archiveName}, expected ${archiveName}`
      );
    }

    verified.push(result);
  }

  return verified;
}

export function parseReleaseChecksumArgs(args: string[]): VerifyReleaseChecksumsOptions & {
  help: boolean;
} {
  const options: VerifyReleaseChecksumsOptions & { help: boolean } = { help: false };

  for (let index = 0; index < args.length; index++) {
    const arg = args[index]!;
    const [flag, inlineValue] = splitFlagValue(arg);

    switch (flag) {
      case '--dir':
        options.releaseDir = readOptionValue(args, inlineValue, ++index, flag);
        if (inlineValue !== undefined) index--;
        break;
      case '--help':
      case '-h':
        rejectInlineValue(flag, inlineValue);
        options.help = true;
        break;
      default:
        if (arg.startsWith('-')) {
          throw new Error(`Unknown option: ${arg}`);
        }
        if (options.releaseDir) {
          throw new Error(`Unexpected argument: ${arg}`);
        }
        options.releaseDir = arg;
    }
  }

  return options;
}

export function releaseChecksumHelp(): string {
  return [
    'Usage: bun run scripts/release-checksums.ts [--dir <path>]',
    '',
    'Verifies that every release archive has a matching .sha256 file and digest.',
    '',
    'Options:',
    '  --dir <path>  Release directory to verify (default: dist/release)',
    '  -h, --help    Show this help',
  ].join('\n');
}

async function main(): Promise<void> {
  try {
    const options = parseReleaseChecksumArgs(process.argv.slice(2));

    if (options.help) {
      console.log(releaseChecksumHelp());
      return;
    }

    const results = await verifyReleaseChecksums(options);
    for (const result of results) {
      console.log(`Verified ${result.archiveName}.sha256 (${result.actualChecksum})`);
    }
    console.log(`Verified ${results.length} release checksum(s)`);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error(`[zero] Release checksum verification failed: ${message}`);
    process.exitCode = 1;
  }
}

function assertSafeChecksumFileName(fileName: string): void {
  if (
    !fileName ||
    fileName !== basename(fileName) ||
    fileName.includes('/') ||
    fileName.includes('\\')
  ) {
    throw new Error(`Checksum archive name must be a same-directory file name: ${fileName}`);
  }
}

function splitFlagValue(arg: string): [string, string | undefined] {
  const separatorIndex = arg.indexOf('=');
  if (separatorIndex === -1) return [arg, undefined];
  return [arg.slice(0, separatorIndex), arg.slice(separatorIndex + 1)];
}

function readOptionValue(
  args: string[],
  inlineValue: string | undefined,
  index: number,
  flag: string
): string {
  if (inlineValue !== undefined) {
    if (inlineValue === '') {
      throw new Error(`${flag} requires a value`);
    }
    return inlineValue;
  }

  const value = args[index];
  if (!value || value.startsWith('--')) {
    throw new Error(`${flag} requires a value`);
  }
  return value;
}

function rejectInlineValue(flag: string, inlineValue: string | undefined): void {
  if (inlineValue !== undefined) {
    throw new Error(`${flag} does not accept a value`);
  }
}

if (import.meta.main) {
  await main();
}
