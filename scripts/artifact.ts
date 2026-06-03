import { join } from 'node:path';

export function getZeroArtifactName(platform = process.platform): string {
  return platform === 'win32' ? 'zero.exe' : 'zero';
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
export const zeroArtifactPath = join(process.cwd(), zeroArtifactName);
