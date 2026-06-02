import { ZERO_RELEASE_REPOSITORY, ZERO_VERSION } from '../version';

type FetchResponse = {
  ok: boolean;
  status: number;
  statusText: string;
  json(): Promise<unknown>;
};

export type UpdateFetch = (
  url: string,
  init?: {
    headers?: Record<string, string>;
  }
) => Promise<FetchResponse>;

export type UpdateCheckResult = {
  currentVersion: string;
  latestVersion: string;
  releaseUrl: string;
  tagName: string;
  updateAvailable: boolean;
};

export type UpdateCheckOptions = {
  currentVersion?: string;
  endpoint?: string;
  fetch?: UpdateFetch;
  repository?: string;
};

type Semver = [major: number, minor: number, patch: number];

function getDefaultEndpoint(repository: string): string {
  return `https://api.github.com/repos/${repository}/releases/latest`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

export function normalizeVersionTag(version: string): string {
  const trimmed = version.trim();
  const match = trimmed.match(/^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/);

  if (!match) {
    throw new Error(`Invalid semantic version: ${version}`);
  }

  return `${Number(match[1])}.${Number(match[2])}.${Number(match[3])}`;
}

function parseSemver(version: string): Semver {
  const normalized = normalizeVersionTag(version);
  const parts = normalized.split('.').map(part => Number(part));

  return [parts[0] ?? 0, parts[1] ?? 0, parts[2] ?? 0];
}

export function compareSemver(left: string, right: string): number {
  const leftParts = parseSemver(left);
  const rightParts = parseSemver(right);

  return (
    leftParts[0] - rightParts[0] ||
    leftParts[1] - rightParts[1] ||
    leftParts[2] - rightParts[2]
  );
}

export async function checkForUpdate(options: UpdateCheckOptions = {}): Promise<UpdateCheckResult> {
  const currentVersion = normalizeVersionTag(options.currentVersion ?? ZERO_VERSION);
  const repository = options.repository ?? ZERO_RELEASE_REPOSITORY;
  const endpoint = options.endpoint ?? process.env.ZERO_UPDATE_RELEASE_URL ?? getDefaultEndpoint(repository);
  const fetchRelease = options.fetch ?? (globalThis.fetch as unknown as UpdateFetch);

  const response = await fetchRelease(endpoint, {
    headers: {
      Accept: 'application/vnd.github+json',
      'User-Agent': `zero/${currentVersion}`,
    },
  });

  if (!response.ok) {
    const status = response.statusText ? `${response.status} ${response.statusText}` : `${response.status}`;
    throw new Error(`GitHub release check failed (${status})`);
  }

  const body = await response.json();

  if (!isRecord(body) || typeof body.tag_name !== 'string') {
    throw new Error('GitHub release response did not include a tag_name');
  }

  const tagName = body.tag_name;
  const latestVersion = normalizeVersionTag(tagName);
  const releaseUrl = typeof body.html_url === 'string'
    ? body.html_url
    : `https://github.com/${repository}/releases/tag/${tagName}`;

  return {
    currentVersion,
    latestVersion,
    releaseUrl,
    tagName,
    updateAvailable: compareSemver(latestVersion, currentVersion) > 0,
  };
}

export function formatUpdateCheck(result: UpdateCheckResult): string {
  if (result.updateAvailable) {
    return [
      `[zero] Update available: ${result.currentVersion} -> ${result.latestVersion}`,
      `Release: ${result.releaseUrl}`,
      'Download the matching release asset for your platform, then replace the current zero binary.',
    ].join('\n');
  }

  return [
    `[zero] zero is up to date (${result.currentVersion})`,
    `Latest release: ${result.releaseUrl}`,
  ].join('\n');
}
