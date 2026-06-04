import { join } from 'node:path';

export function getZeroArtifactName(platform = process.platform): string {
  return platform === 'win32' ? 'zero.exe' : 'zero';
}

export function getZeroArtifactNameForGoOS(goos: string): string {
  return goos === 'windows' ? 'zero.exe' : 'zero';
}

export function getZeroArtifactPath(platform = process.platform, cwd = process.cwd()): string {
  return join(cwd, getZeroArtifactName(platform));
}

export function getGoOS(platform = process.platform): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'darwin';
  return platform;
}

export function getGoArch(arch = process.arch): string {
  if (arch === 'x64') return 'amd64';
  if (arch === 'ia32') return '386';
  return arch;
}

export function getReleasePlatform(platform = process.platform): string {
  if (platform === 'darwin') return 'macos';
  if (platform === 'win32') return 'windows';
  return platform;
}

export function getReleaseArchiveExtension(platform = process.platform): string {
  return platform === 'win32' ? 'zip' : 'tar.gz';
}

export function getReleasePackageName(
  version: string,
  platform = process.platform,
  arch = process.arch,
): string {
  return `zero-v${version}-${getReleasePlatform(platform)}-${arch}`;
}

export function getReleaseArchiveName(
  version: string,
  platform = process.platform,
  arch = process.arch,
): string {
  return `${getReleasePackageName(version, platform, arch)}.${getReleaseArchiveExtension(platform)}`;
}

export const zeroArtifactName = getZeroArtifactName();
export const zeroArtifactPath = getZeroArtifactPath();
