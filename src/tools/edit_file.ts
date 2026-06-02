import { z } from 'zod';
import { readFile, writeFile } from 'fs/promises';
import type { Tool } from './types';

const EditFileParams = z.object({
  path: z.string().min(1).describe('Path of the file to edit.'),
  old_string: z.string().min(1).describe('The exact string to replace. Must match the file byte-for-byte.'),
  new_string: z.string().describe('The replacement string. May be empty to delete a region.'),
  replace_all: z
    .boolean()
    .optional()
    .default(false)
    .describe('If true, replace every occurrence of old_string. Defaults to false (replace first match only).'),
});

/**
 * Exact-string edit.
 *
 * By default the old_string must appear exactly once in the file —
 * this is the safety property that prevents the model from making
 * "global" edits it didn't intend. Setting `replace_all: true` opts
 * out of that check and replaces every occurrence.
 */
export const editFileTool: Tool = {
  name: 'edit_file',
  description:
    'Replace an exact string in a file. By default old_string must match exactly one location (a safety check). ' +
    'Pass replace_all: true to replace every occurrence. The new_string may be empty to delete a region.',
  parameters: EditFileParams,
  async execute(args) {
    const { path, old_string, new_string, replace_all } = EditFileParams.parse(args);

    let content: string;
    try {
      content = await readFile(path, 'utf-8');
    } catch (err: any) {
      return `Error reading ${path}: ${err.message}`;
    }

    const occurrences = content.split(old_string).length - 1;

    if (occurrences === 0) {
      return `Error: Could not find the exact string to replace in ${path}. The old_string must match the file byte-for-byte.`;
    }

    if (!replace_all && occurrences > 1) {
      return `Error: old_string matches ${occurrences} locations in ${path}. ` +
        `Either make old_string more specific, or pass replace_all: true to replace every occurrence.`;
    }

    const updated = replace_all
      ? content.split(old_string).join(new_string)
      : content.replace(old_string, new_string);

    if (updated === content) {
      return `No changes: new_string is identical to old_string.`;
    }

    try {
      await writeFile(path, updated, 'utf-8');
      const replacedCount = replace_all ? occurrences : 1;
      return `Successfully edited ${path} (replaced ${replacedCount} occurrence${replacedCount === 1 ? '' : 's'}).`;
    } catch (err: any) {
      return `Error writing ${path}: ${err.message}`;
    }
  },
};
