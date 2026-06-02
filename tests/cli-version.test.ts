import { describe, expect, it } from 'bun:test';

describe('zero --version', () => {
  it('prints the package version', async () => {
    const packageJson = await Bun.file('package.json').json() as { version: string };
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
    expect(stdout.trim()).toBe(packageJson.version);
  });
});

describe('zero update --check', () => {
  it('prints the latest release status from the configured endpoint', async () => {
    const release = encodeURIComponent(JSON.stringify({
      tag_name: 'v0.2.0',
      html_url: 'https://github.com/Gitlawb/zero/releases/tag/v0.2.0',
    }));
    const child = Bun.spawn([process.execPath, 'src/index.ts', 'update', '--check'], {
      env: {
        ...process.env,
        ZERO_UPDATE_RELEASE_URL: `data:application/json,${release}`,
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
    expect(stdout).toContain('Update available: 0.1.0 -> 0.2.0');
  });
});
