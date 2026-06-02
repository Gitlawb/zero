import { describe, expect, it } from 'bun:test';
import {
  checkForUpdate,
  compareSemver,
  formatUpdateCheck,
  normalizeVersionTag,
  type UpdateFetch,
} from '../src/update/check';

function releaseFetch(body: unknown): UpdateFetch {
  return async () => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    json: async () => body,
  });
}

describe('update version helpers', () => {
  it('normalizes GitHub release tags into semantic versions', () => {
    expect(normalizeVersionTag('v0.2.0')).toBe('0.2.0');
    expect(normalizeVersionTag('0.2.0')).toBe('0.2.0');
    expect(normalizeVersionTag('v1.2.3+build.4')).toBe('1.2.3');
  });

  it('compares semantic versions by major, minor, and patch', () => {
    expect(compareSemver('0.2.0', '0.1.9')).toBeGreaterThan(0);
    expect(compareSemver('1.0.0', '0.99.99')).toBeGreaterThan(0);
    expect(compareSemver('0.1.1', '0.1.2')).toBeLessThan(0);
    expect(compareSemver('v0.1.0', '0.1.0')).toBe(0);
  });
});

describe('checkForUpdate', () => {
  it('reports an available update from the latest release', async () => {
    const result = await checkForUpdate({
      currentVersion: '0.1.0',
      fetch: releaseFetch({
        tag_name: 'v0.2.0',
        html_url: 'https://github.com/Gitlawb/zero/releases/tag/v0.2.0',
      }),
    });

    expect(result).toEqual({
      currentVersion: '0.1.0',
      latestVersion: '0.2.0',
      releaseUrl: 'https://github.com/Gitlawb/zero/releases/tag/v0.2.0',
      tagName: 'v0.2.0',
      updateAvailable: true,
    });
  });

  it('reports up to date when the latest release matches the current version', async () => {
    const result = await checkForUpdate({
      currentVersion: '0.2.0',
      fetch: releaseFetch({
        tag_name: 'v0.2.0',
        html_url: 'https://github.com/Gitlawb/zero/releases/tag/v0.2.0',
      }),
    });

    expect(result.updateAvailable).toBe(false);
  });

  it('throws on malformed release payloads', async () => {
    await expect(checkForUpdate({
      fetch: releaseFetch({ name: 'Zero 0.2.0' }),
    })).rejects.toThrow('tag_name');
  });

  it('formats human-readable update output', () => {
    expect(formatUpdateCheck({
      currentVersion: '0.1.0',
      latestVersion: '0.2.0',
      releaseUrl: 'https://github.com/Gitlawb/zero/releases/tag/v0.2.0',
      tagName: 'v0.2.0',
      updateAvailable: true,
    })).toContain('Update available: 0.1.0 -> 0.2.0');
  });
});
