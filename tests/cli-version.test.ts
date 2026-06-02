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
