import { join } from 'node:path';

export function getZeroArtifactName(platform = process.platform): string {
  return platform === 'win32' ? 'zero.exe' : 'zero';
}

export const zeroArtifactName = getZeroArtifactName();
export const zeroArtifactPath = join(process.cwd(), zeroArtifactName);
