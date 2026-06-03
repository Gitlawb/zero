import { describe, expect, it } from 'bun:test';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  formatSha256Checksum,
  parseReleaseChecksumArgs,
  parseSha256Checksum,
  sha256File,
  verifyReleaseChecksums,
  verifySha256Checksum,
  writeSha256Checksum,
} from '../scripts/release-checksums';

async function withTempDir<T>(fn: (dir: string) => Promise<T>): Promise<T> {
  const dir = await mkdtemp(join(tmpdir(), 'zero-release-checksums-'));
  try {
    return await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

describe('release checksum helpers', () => {
  it('hashes archive bytes as SHA-256 hex', async () => {
    await withTempDir(async (dir) => {
      const archivePath = join(dir, 'zero-v0.1.0-linux-x64.tar.gz');
      const archiveBytes = 'zero archive bytes';
      await writeFile(archivePath, archiveBytes, 'utf-8');

      const expected = createHash('sha256').update(archiveBytes).digest('hex');

      expect(await sha256File(archivePath)).toBe(expected);
    });
  });

  it('writes installer-compatible SHA-256 files and verifies release directories', async () => {
    await withTempDir(async (dir) => {
      const archiveName = 'zero-v0.1.0-linux-x64.tar.gz';
      const archivePath = join(dir, archiveName);
      await writeFile(archivePath, 'zero archive bytes', 'utf-8');

      const written = await writeSha256Checksum(archivePath);
      const checksumText = await readFile(written.checksumPath, 'utf-8');

      expect(checksumText).toMatch(new RegExp(`^[a-f0-9]{64}  ${archiveName}\\n$`));

      const verifiedFile = await verifySha256Checksum(written.checksumPath);
      expect(verifiedFile.archiveName).toBe(archiveName);
      expect(verifiedFile.expectedChecksum).toBe(verifiedFile.actualChecksum);

      const verifiedRelease = await verifyReleaseChecksums({ releaseDir: dir });
      expect(verifiedRelease).toHaveLength(1);
      expect(verifiedRelease[0]?.archiveName).toBe(archiveName);
    });
  });

  it('rejects malformed checksum files and unsafe archive names', () => {
    expect(() => parseSha256Checksum('not a checksum')).toThrow(
      'Checksum file must contain'
    );
    expect(() => formatSha256Checksum('abc', 'zero.tar.gz')).toThrow(
      '64 hexadecimal characters'
    );
    expect(() => parseSha256Checksum(`${'a'.repeat(64)}  ../zero.tar.gz\n`)).toThrow(
      'same-directory file name'
    );
    expect(() =>
      parseSha256Checksum(`${'a'.repeat(64)}  zero.tar.gz\n${'b'.repeat(64)}  other.tar.gz\n`)
    ).toThrow('exactly one checksum line');
  });

  it('detects archive changes after checksum generation', async () => {
    await withTempDir(async (dir) => {
      const archivePath = join(dir, 'zero-v0.1.0-linux-x64.tar.gz');
      await writeFile(archivePath, 'original bytes', 'utf-8');
      const written = await writeSha256Checksum(archivePath);

      await writeFile(archivePath, 'changed bytes', 'utf-8');

      await expect(verifySha256Checksum(written.checksumPath)).rejects.toThrow(
        'Checksum mismatch'
      );
    });
  });

  it('requires each release archive to have exactly one matching checksum file', async () => {
    await withTempDir(async (dir) => {
      const archivePath = join(dir, 'zero-v0.1.0-linux-x64.tar.gz');
      await writeFile(archivePath, 'archive bytes', 'utf-8');

      await expect(verifyReleaseChecksums({ releaseDir: dir })).rejects.toThrow(
        'Missing checksum file'
      );

      await writeSha256Checksum(archivePath);
      await writeFile(
        join(dir, 'zero-v0.1.0-macos-arm64.tar.gz.sha256'),
        `${'a'.repeat(64)}  zero-v0.1.0-macos-arm64.tar.gz\n`,
        'utf-8'
      );

      await expect(verifyReleaseChecksums({ releaseDir: dir })).rejects.toThrow(
        'Unexpected checksum file'
      );
    });
  });

  it('rejects empty release directories', async () => {
    await withTempDir(async (dir) => {
      await expect(verifyReleaseChecksums({ releaseDir: dir })).rejects.toThrow(
        'No release archives found'
      );
    });
  });

  it('parses verifier CLI arguments', () => {
    expect(parseReleaseChecksumArgs(['--dir=dist/release'])).toEqual({
      releaseDir: 'dist/release',
      help: false,
    });
    expect(parseReleaseChecksumArgs(['dist/release'])).toEqual({
      releaseDir: 'dist/release',
      help: false,
    });
    expect(parseReleaseChecksumArgs(['--help'])).toEqual({ help: true });
  });
});
