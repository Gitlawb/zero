import { join } from 'node:path';

export const zeroArtifactName = process.platform === 'win32' ? 'zero.exe' : 'zero';
export const zeroArtifactPath = join(process.cwd(), zeroArtifactName);
