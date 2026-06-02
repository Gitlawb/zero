import { describe, expect, it } from 'bun:test';

type ReleasePayload = {
  tag_name: string;
  html_url: string;
};

async function packageVersion(): Promise<string> {
  const packageJson = await Bun.file('package.json').json() as { version: string };
  return packageJson.version;
}

function nextPatchVersion(version: string): string {
  const match = version.match(/^(\d+)\.(\d+)\.(\d+)$/);

  if (!match) {
    throw new Error(`Invalid package version: ${version}`);
  }

  return `${Number(match[1])}.${Number(match[2])}.${Number(match[3]) + 1}`;
}

function releaseEndpoint(payload: ReleasePayload): string {
  return `data:application/json,${encodeURIComponent(JSON.stringify(payload))}`;
}

describe('zero --version', () => {
  it('prints the package version', async () => {
    const version = await packageVersion();
    const child = Bun.spawn([process.execPath, 'src/index.ts', '--version'], {
      stderr: 'pipe',
      stdout: 'pipe',
    });

    const [exitCode, stdout, stderr] = await Promise.all([
      child.exited,
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
    ]);

    expect(exitCode).toBe(0);
    expect(stderr.trim()).toBe('');
    expect(stdout.trim()).toBe(version);
  });
});

describe('zero update --check', () => {
  it('prints the latest release status from the configured endpoint', async () => {
    const currentVersion = await packageVersion();
    const latestVersion = nextPatchVersion(currentVersion);
    const child = Bun.spawn([process.execPath, 'src/index.ts', 'update', '--check'], {
      env: {
        ...process.env,
        ZERO_UPDATE_RELEASE_URL: releaseEndpoint({
          tag_name: `v${latestVersion}`,
          html_url: `https://github.com/Gitlawb/zero/releases/tag/v${latestVersion}`,
        }),
      },
      stderr: 'pipe',
      stdout: 'pipe',
    });

    const [exitCode, stdout, stderr] = await Promise.all([
      child.exited,
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
    ]);

    expect(exitCode).toBe(0);
    expect(stderr.trim()).toBe('');
    expect(stdout).toContain(`Update available: ${currentVersion} -> ${latestVersion}`);
  });

  it('prints JSON output for automation', async () => {
    const currentVersion = await packageVersion();
    const child = Bun.spawn([process.execPath, 'src/index.ts', 'update', '--check', '--json'], {
      env: {
        ...process.env,
        ZERO_UPDATE_RELEASE_URL: releaseEndpoint({
          tag_name: `v${currentVersion}`,
          html_url: `https://github.com/Gitlawb/zero/releases/tag/v${currentVersion}`,
        }),
      },
      stderr: 'pipe',
      stdout: 'pipe',
    });

    const [exitCode, stdout, stderr] = await Promise.all([
      child.exited,
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
    ]);

    const parsed = JSON.parse(stdout) as { currentVersion: string; updateAvailable: boolean };

    expect(exitCode).toBe(0);
    expect(stderr.trim()).toBe('');
    expect(parsed.currentVersion).toBe(currentVersion);
    expect(parsed.updateAvailable).toBe(false);
  });
});
