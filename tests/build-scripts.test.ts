import { describe, expect, it } from 'bun:test';
import { getZeroArtifactName } from '../scripts/artifact';

describe('build artifact naming', () => {
  it('uses a Windows executable suffix on win32', () => {
    expect(getZeroArtifactName('win32')).toBe('zero.exe');
  });

  it('uses the plain binary name on Unix platforms', () => {
    expect(getZeroArtifactName('linux')).toBe('zero');
    expect(getZeroArtifactName('darwin')).toBe('zero');
  });
});
