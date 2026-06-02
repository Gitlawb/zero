import { z } from 'zod';
import { writeFile, mkdir, stat } from 'fs/promises';
import { dirname } from 'path';
import type { Tool } from './types';

const WriteFileParams = z.object({
  path: z.string().min(1).describe('Absolute or relative path of the file to write.'),
  content: z.string().describe('Full file contents to write.'),
  overwrite: z
    .boolean()
    .optional()
    .default(false)
    .describe('If true, allow overwriting an existing file. Defaults to false.'),
});

export const writeFileTool: Tool = {
  name: 'write_file',
  description:
    'Create a new file with the given contents. Refuses to overwrite existing files unless `overwrite: true` is passed. ' +
    'Parent directories are created automatically. Use this for new files; for existing files prefer `edit_file`.',
  parameters: WriteFileParams,
  async execute(args) {
    const { path, content, overwrite } = WriteFileParams.parse(args);

    // Use stat() for an existence check: a zero-byte file should still be
    // considered existing, otherwise write_file would silently overwrite
    // touched-but-empty files.
    let existed = false;
    try {
      await stat(path);
      existed = true;
      if (!overwrite) {
        return `Error: ${path} already exists. Pass overwrite: true to replace it.`;
      }
    } catch {
      // File does not exist; safe to create.
    }

    try {
      await mkdir(dirname(path), { recursive: true });
      await writeFile(path, content, 'utf-8');
      return existed
        ? `Overwrote ${path} (${content.length} bytes).`
        : `Created ${path} (${content.length} bytes).`;
    } catch (err: any) {
      return `Error writing file ${path}: ${err.message}`;
    }
  },
};
