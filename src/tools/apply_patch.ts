import { mkdtemp, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import { z } from 'zod';
import { execa } from 'execa';
import type { Tool } from './types';

const ApplyPatchParams = z.object({
  patch: z.string().min(1).describe('Unified diff patch to apply.'),
  cwd: z.string().optional().describe('Directory where the patch should be applied. Defaults to the current workspace.'),
});

export const applyPatchTool: Tool = {
  name: 'apply_patch',
  description: 'Apply a unified diff patch to files in the workspace.',
  parameters: ApplyPatchParams,
  safety: {
    sideEffect: 'write',
    permission: 'prompt',
    reason: 'Applies patch hunks that can create, edit, or delete files.',
  },
  async execute(args) {
    const { patch, cwd } = ApplyPatchParams.parse(args);
    const root = cwd || process.cwd();
    const tempDir = await mkdtemp(join(tmpdir(), 'zero-patch-'));
    const patchPath = join(tempDir, 'change.patch');

    try {
      await writeFile(patchPath, patch, 'utf-8');

      const result = await execa('git', ['apply', '--whitespace=nowarn', patchPath], {
        cwd: root,
        reject: false,
      });

      if (result.exitCode !== 0) {
        const output = [result.stderr, result.stdout].filter(Boolean).join('\n').trim();
        return `Error applying patch: ${output || `git apply exited with code ${result.exitCode}`}`;
      }

      return 'Patch applied successfully.';
    } catch (err: any) {
      return `Error applying patch: ${err.message}`;
    } finally {
      await rm(tempDir, { recursive: true, force: true });
    }
  },
};
