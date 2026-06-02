import { describe, expect, it } from 'bun:test';
import {
  getReleaseArchiveExtension,
  getReleaseArchiveName,
  getReleasePackageName,
  getReleasePlatform,
  getZeroArtifactName,
} from '../scripts/artifact';

describe('build artifact naming', () => {
  it('uses a Windows executable suffix on win32', () => {
    expect(getZeroArtifactName('win32')).toBe('zero.exe');
  });

  it('uses the plain binary name on Unix platforms', () => {
    expect(getZeroArtifactName('linux')).toBe('zero');
    expect(getZeroArtifactName('darwin')).toBe('zero');
  });
});

describe('release artifact naming', () => {
  it('normalizes package platform names', () => {
    expect(getReleasePlatform('darwin')).toBe('macos');
    expect(getReleasePlatform('win32')).toBe('windows');
    expect(getReleasePlatform('linux')).toBe('linux');
  });

  it('uses zip for Windows and tar.gz elsewhere', () => {
    expect(getReleaseArchiveExtension('win32')).toBe('zip');
    expect(getReleaseArchiveExtension('linux')).toBe('tar.gz');
    expect(getReleaseArchiveExtension('darwin')).toBe('tar.gz');
  });

  it('includes version, platform, and architecture in release names', () => {
    expect(getReleasePackageName('0.1.0', 'darwin', 'arm64')).toBe('zero-v0.1.0-macos-arm64');
    expect(getReleaseArchiveName('0.1.0', 'win32', 'x64')).toBe('zero-v0.1.0-windows-x64.zip');
    expect(getReleaseArchiveName('0.1.0', 'linux', 'x64')).toBe('zero-v0.1.0-linux-x64.tar.gz');
  });
});
