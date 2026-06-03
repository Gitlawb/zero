import { describe, expect, it } from 'bun:test';
import { createHash } from 'node:crypto';
import { chmod, mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

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
    expect(script).toContain("curl --fail --location --show-error --silent --header 'Accept: application/vnd.github+json'");
    expect(script).toContain('verify_checksum "$checksum_name"');
    expect(script).toContain('tar -xzf "$archive_path" -C "$extract_dir"');
    expect(script).toContain('find_extracted_binary "$extract_dir"');
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
    expect(script).toContain('Get-ChildItem -Path $extractDir -Filter "zero.exe" -File -Recurse');
    expect(script).toContain('Copy-Item -Path $binaryPath -Destination $targetPath -Force');
  });

  it('installs from a prefixed Unix release archive without network access', async () => {
    if (process.platform === 'win32') return;

    const releasePlatform = process.platform === 'darwin' ? 'macos' : 'linux';
    const releaseArch = process.arch === 'arm64' ? 'arm64' : 'x64';
    const packageName = `zero-v0.1.0-${releasePlatform}-${releaseArch}`;
    const archiveName = `${packageName}.tar.gz`;
    const root = await mkdtemp(join(tmpdir(), 'zero-install-test-'));
    const mockBin = join(root, 'bin');
    const packageDir = join(root, 'package', packageName);
    const releaseDir = join(root, 'release');
    const installDir = join(root, 'install');
    const archivePath = join(releaseDir, archiveName);
    const checksumPath = `${archivePath}.sha256`;

    await mkdir(mockBin, { recursive: true });
    await mkdir(packageDir, { recursive: true });
    await mkdir(releaseDir, { recursive: true });
    await mkdir(installDir, { recursive: true });
    await writeFile(join(packageDir, 'zero'), '#!/usr/bin/env sh\necho mock-zero\n');
    await chmod(join(packageDir, 'zero'), 0o755);

    const tar = Bun.spawn(['tar', '-C', join(root, 'package'), '-czf', archivePath, packageName], {
      stderr: 'pipe',
      stdout: 'pipe',
    });
    const [tarExit, tarStderr] = await Promise.all([
      tar.exited,
      new Response(tar.stderr).text(),
    ]);
    expect(tarExit).toBe(0);
    expect(tarStderr.trim()).toBe('');

    const checksum = createHash('sha256')
      .update(Buffer.from(await Bun.file(archivePath).arrayBuffer()))
      .digest('hex');
    await writeFile(checksumPath, `${checksum}  ${archiveName}\n`);

    const mockCurl = join(mockBin, 'curl');
    await writeFile(mockCurl, `#!/usr/bin/env sh
set -eu
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      output="$2"
      shift 2
      ;;
    --header)
      shift 2
      ;;
    --fail|--location|--show-error|--silent)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

case "$url" in
  */${archiveName})
    cp "${archivePath}" "$output"
    ;;
  */${archiveName}.sha256)
    cp "${checksumPath}" "$output"
    ;;
  *)
    echo "unexpected url: $url" >&2
    exit 2
    ;;
esac
`);
    await chmod(mockCurl, 0o755);

    const child = Bun.spawn(['bash', 'scripts/install.sh', '--version', '0.1.0', '--install-dir', installDir], {
      env: {
        ...process.env,
        PATH: `${mockBin}:${process.env.PATH ?? ''}`,
        ZERO_GITHUB_BASE_URL: 'https://example.test',
        ZERO_REPO: 'Gitlawb/zero',
      },
      stderr: 'pipe',
      stdout: 'pipe',
    });
    const [exitCode, stdout, stderr] = await Promise.all([
      child.exited,
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
    ]);

    expect(exitCode).toBe(0);
    expect(stderr.trim()).toBe('');
    expect(stdout).toContain(`Installed ${join(installDir, 'zero')}`);
    expect(await Bun.file(join(installDir, 'zero')).text()).toContain('mock-zero');
  });
});
