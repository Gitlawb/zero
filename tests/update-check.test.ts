import { describe, expect, it } from 'bun:test';
import pkg from '../package.json' with { type: 'json' };
import {
  checkForUpdate,
  compareSemver,
  formatUpdateCheck,
  getReleaseEndpoint,
  normalizeVersionTag,
  resolveReleaseEndpoint,
  type UpdateFetch,
} from '../src/update/check';
import { ZERO_VERSION } from '../src/version';

function releaseFetch(
  body: unknown,
  onRequest?: (url: string, init?: Parameters<UpdateFetch>[1]) => void
): UpdateFetch {
  return async (url, init) => {
    onRequest?.(url, init);
    return {
      ok: true,
      status: 200,
      statusText: 'OK',
      json: async () => body,
    };
  };
}

describe('update version helpers', () => {
  it('uses package.json as the CLI version source of truth', () => {
    expect(ZERO_VERSION).toBe(pkg.version);
  });

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

  it('passes an abort signal to fetch when timeoutMs is enabled', async () => {
    let signal: AbortSignal | undefined;

    await checkForUpdate({
      currentVersion: '0.1.0',
      timeoutMs: 5000,
      fetch: releaseFetch({
        tag_name: 'v0.1.0',
        html_url: 'https://github.com/Gitlawb/zero/releases/tag/v0.1.0',
      }, (_url, init) => {
        signal = init?.signal;
      }),
    });

    expect(signal).toBeInstanceOf(AbortSignal);
    expect(signal?.aborted).toBe(false);
  });

  it('resolves update endpoint precedence and owner/repo slugs', async () => {
    const previous = process.env.ZERO_UPDATE_RELEASE_URL;
    const urls: string[] = [];
    const fetch = releaseFetch({
      tag_name: 'v0.1.0',
      html_url: 'https://github.com/Gitlawb/zero/releases/tag/v0.1.0',
    }, (url) => {
      urls.push(url);
    });

    try {
      process.env.ZERO_UPDATE_RELEASE_URL = 'Gitlawb/env-zero';

      await checkForUpdate({ endpoint: 'Gitlawb/option-zero', fetch, timeoutMs: 0 });
      expect(urls.pop()).toBe(getReleaseEndpoint('Gitlawb/option-zero'));

      await checkForUpdate({ repository: 'Gitlawb/repo-zero', fetch, timeoutMs: 0 });
      expect(urls.pop()).toBe(getReleaseEndpoint('Gitlawb/env-zero'));

      delete process.env.ZERO_UPDATE_RELEASE_URL;
      await checkForUpdate({ repository: 'Gitlawb/repo-zero', fetch, timeoutMs: 0 });
      expect(urls.pop()).toBe(getReleaseEndpoint('Gitlawb/repo-zero'));
    } finally {
      if (previous === undefined) {
        delete process.env.ZERO_UPDATE_RELEASE_URL;
      } else {
        process.env.ZERO_UPDATE_RELEASE_URL = previous;
      }
    }
  });

  it('accepts full endpoint URLs and rejects invalid endpoint values', () => {
    expect(resolveReleaseEndpoint('https://example.test/latest', 'Gitlawb/zero')).toBe('https://example.test/latest');
    expect(resolveReleaseEndpoint('Gitlawb/zero', 'Fallback/repo')).toBe(getReleaseEndpoint('Gitlawb/zero'));
    expect(() => resolveReleaseEndpoint('not-a-url', 'Gitlawb/zero')).toThrow('Invalid update endpoint');
  });
});
