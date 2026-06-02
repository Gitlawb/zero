import { z } from 'zod';
import type { Tool } from './types';

const GlobParams = z.object({
  pattern: z.string().min(1).describe('Glob pattern to match, for example "**/*.ts".'),
  cwd: z.string().optional().describe('Directory to scan. Defaults to the current working directory.'),
  limit: z.number().int().min(1).max(1000).optional().default(100).describe('Maximum matches to return.'),
  include_dirs: z.boolean().optional().default(false).describe('Whether directory matches should be included.'),
});

export const globTool: Tool = {
  name: 'glob',
  description: 'Find files by glob pattern. Use this to quickly discover files by name or extension.',
  parameters: GlobParams,
  safety: {
    sideEffect: 'read',
    permission: 'allow',
    reason: 'Finds matching paths without reading contents or modifying files.',
  },
  async execute(args) {
    const { pattern, cwd, limit, include_dirs } = GlobParams.parse(args);
    const glob = new Bun.Glob(pattern);
    const root = cwd || process.cwd();
    const matches: string[] = [];

    try {
      for (const match of glob.scanSync({ cwd: root, onlyFiles: !include_dirs })) {
        matches.push(match);
        if (matches.length > limit) break;
      }
    } catch (err: any) {
      return `Error running glob "${pattern}": ${err.message}`;
    }

    if (matches.length === 0) {
      return `No matches found for ${pattern}`;
    }

    const visible = matches.slice(0, limit);
    const truncated = matches.length > limit ? `\n... truncated after ${limit} matches` : '';
    return visible.join('\n') + truncated;
  },
};
