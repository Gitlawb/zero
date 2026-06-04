import { describe, expect, it } from 'bun:test';
import { join } from 'node:path';
import {
  getGoArch,
  getGoOS,
  getReleaseArchiveExtension,
  getReleaseArchiveName,
  getReleasePackageName,
  getReleasePlatform,
  getZeroArtifactName,
  getZeroArtifactNameForGoOS,
} from '../scripts/artifact';
import {
  defaultBuildOutput,
  goBuildLdflags,
  parseBuildArgs,
  parsePackageVersion,
} from '../scripts/build';

describe('build artifact naming', () => {
  it('uses a Windows executable suffix on win32', () => {
    expect(getZeroArtifactName('win32')).toBe('zero.exe');
  });

  it('uses the plain binary name on Unix platforms', () => {
    expect(getZeroArtifactName('linux')).toBe('zero');
    expect(getZeroArtifactName('darwin')).toBe('zero');
  });

  it('maps Go build targets to executable names', () => {
    expect(getZeroArtifactNameForGoOS('windows')).toBe('zero.exe');
    expect(getZeroArtifactNameForGoOS('linux')).toBe('zero');
    expect(getZeroArtifactNameForGoOS('darwin')).toBe('zero');
  });

  it('maps Node platform and architecture names to Go targets', () => {
    expect(getGoOS('win32')).toBe('windows');
    expect(getGoOS('darwin')).toBe('darwin');
    expect(getGoOS('linux')).toBe('linux');
    expect(getGoArch('x64')).toBe('amd64');
    expect(getGoArch('arm64')).toBe('arm64');
    expect(getGoArch('ia32')).toBe('386');
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

describe('Go binary build script', () => {
  it('parses target overrides from environment and CLI flags', () => {
    expect(parseBuildArgs([], {
      ZERO_BUILD_GOOS: 'linux',
      ZERO_BUILD_GOARCH: 'arm64',
    })).toMatchObject({
      goos: 'linux',
      goarch: 'arm64',
      help: false,
    });

    expect(parseBuildArgs(['--goos=windows', '--goarch', 'amd64', '--output', 'dist/zero.exe'], {}))
      .toEqual({
        goos: 'windows',
        goarch: 'amd64',
        output: 'dist/zero.exe',
        help: false,
      });
  });

  it('rejects flag-shaped values for options that require values', () => {
    expect(() => parseBuildArgs(['-o', '-h'])).toThrow('-o requires a value');
    expect(() => parseBuildArgs(['--goarch', '-h'])).toThrow('--goarch requires a value');
  });

  it('builds the expected default output path for the target OS', () => {
    expect(defaultBuildOutput('windows', '/repo')).toBe(join('/repo', 'zero.exe'));
    expect(defaultBuildOutput('linux', '/repo')).toBe(join('/repo', 'zero'));
  });

  it('injects the package version into the Go CLI package', () => {
    expect(parsePackageVersion('{"version":"0.1.0"}')).toBe('0.1.0');
    expect(goBuildLdflags('0.1.0')).toContain(
      '-X github.com/Gitlawb/zero/internal/cli.version=0.1.0'
    );
  });
});
