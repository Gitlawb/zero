import { z } from 'zod';
import { execa } from 'execa';
import type { Tool } from './types';

const BashParams = z.object({
  command: z.string().min(1),
  cwd: z.string().optional(),
});

export const bashTool: Tool = {
  name: 'bash',
  description:
    'Execute a shell command and return the output. Use for running commands, git, tests, etc. ' +
    'No command allowlist exists yet, so only run conservative workspace-safe commands after permission is granted.',
  parameters: BashParams,
  safety: {
    sideEffect: 'shell',
    permission: 'prompt',
    reason: 'Shell commands can read, write, or execute programs.',
  },
  async execute(args) {
    const { command, cwd } = BashParams.parse(args);

    try {
      const result = await execa(command, {
        cwd: cwd || process.cwd(),
        shell: true,
        timeout: 120_000, // 2 minutes
      });

      let output = '';
      if (result.stdout) output += `stdout:\n${result.stdout}\n`;
      if (result.stderr) output += `stderr:\n${result.stderr}\n`;
      if (result.exitCode !== 0) output += `Exit code: ${result.exitCode}`;

      return output.trim() || 'Command completed with no output.';
    } catch (err: any) {
      if (err.timedOut) {
        return 'Error: Command timed out after 2 minutes.';
      }
      return `Error executing command: ${err.message}`;
    }
  },
};
