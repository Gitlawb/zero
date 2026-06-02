import { mkdtemp, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import { isAbsolute, join, relative, resolve } from 'path';
import { z } from 'zod';
import { execa } from 'execa';
import type { Tool } from './types';

const ApplyPatchParams = z.object({
  patch: z.string().min(1).describe('Unified diff patch to apply.'),
  cwd: z.string().optional().describe('Directory where the patch should be applied. Defaults to the current workspace.'),
});

export const applyPatchTool: Tool = {
  name: 'apply_patch',
  description:
    'Apply a unified diff patch inside the current workspace. Paths outside the workspace are rejected; be conservative about which paths you patch.',
  parameters: ApplyPatchParams,
  safety: {
    sideEffect: 'write',
    permission: 'prompt',
    reason: 'Applies patch hunks that can create, edit, or delete files.',
  },
  async execute(args) {
    const { patch, cwd } = ApplyPatchParams.parse(args);
    const workspaceRoot = process.cwd();
    const root = resolve(cwd || workspaceRoot);
    const rel = relative(workspaceRoot, root);

    if (rel.startsWith('..') || isAbsolute(rel)) {
      return `Error applying patch: cwd must stay inside the workspace (${workspaceRoot}).`;
    }

    const tempDir = await mkdtemp(join(tmpdir(), 'zero-patch-'));
    const patchPath = join(tempDir, 'change.patch');

    try {
      await writeFile(patchPath, patch, 'utf-8');

      const gitArgs = ['apply', '--whitespace=nowarn'];
      if (rel) {
        gitArgs.push(`--directory=${rel.replaceAll('\\', '/')}`);
      }
      gitArgs.push(patchPath);

      const result = await execa('git', gitArgs, {
        cwd: workspaceRoot,
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
