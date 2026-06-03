import { describe, expect, it } from 'bun:test';

async function read(path: string): Promise<string> {
  return Bun.file(path).text();
}

describe('install scripts', () => {
  it('keeps the Unix installer aligned with release artifact naming and checksums', async () => {
    const script = await read('scripts/install.sh');

    expect(script.startsWith('#!/usr/bin/env bash')).toBe(true);
    expect(script).toContain('set -euo pipefail');
    expect(script).toContain('ZERO_REPO="${ZERO_REPO:-Gitlawb/zero}"');
    expect(script).toContain('ZERO_INSTALL_DIR="${ZERO_INSTALL_DIR:-$HOME/.local/bin}"');
    expect(script).toContain('archive_name="zero-v${version}-${platform}-${arch}.tar.gz"');
    expect(script).toContain('checksum_name="${archive_name}.sha256"');
    expect(script).toContain('verify_checksum "$checksum_name"');
    expect(script).toContain('tar -xzf "$archive_path" -C "$extract_dir"');
    expect(script).toContain('cp "$binary_path" "$ZERO_INSTALL_DIR/zero"');
  });

  it('keeps the PowerShell installer aligned with Windows release artifacts and checksums', async () => {
    const script = await read('scripts/install.ps1');

    expect(script).toContain('[string]$Repository = $(if ($env:ZERO_REPO)');
    expect(script).toContain('Join-Path $env:LOCALAPPDATA "zero\\bin"');
    expect(script).toContain('$archiveName = "zero-v$releaseVersion-windows-$arch.zip"');
    expect(script).toContain('$checksumName = "$archiveName.sha256"');
    expect(script).toContain('Get-FileHash -Path $archivePath -Algorithm SHA256');
    expect(script).toContain('Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force');
    expect(script).toContain('Copy-Item -Path $binaryPath -Destination $targetPath -Force');
  });
});
