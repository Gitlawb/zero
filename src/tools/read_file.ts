import { z } from 'zod';
import { readFile } from 'fs/promises';
import type { Tool } from './types';

const ReadFileParams = z.object({
  path: z.string().min(1).describe('Path of the file to read.'),
  start_line: z
    .number()
    .int()
    .min(1)
    .optional()
    .describe('1-based inclusive line number to start reading from. Defaults to 1.'),
  end_line: z
    .number()
    .int()
    .min(1)
    .optional()
    .describe('1-based inclusive line number to stop reading at. Defaults to the end of the file.'),
  max_lines: z
    .number()
    .int()
    .min(1)
    .optional()
    .describe('Optional cap on the number of lines returned (applies after start_line).'),
});

/**
 * Read a file (or a slice of it) from disk.
 *
 * Supports 1-based inclusive `start_line` and `end_line` ranges so the
 * agent can grab a specific region without reading a whole large file.
 * Output is always line-numbered so the model can reference exact lines
 * back to the user.
 */
export const readFileTool: Tool = {
  name: 'read_file',
  description:
    'Read the contents of a file. Supports an optional inclusive line range (start_line, end_line) and a max_lines cap. ' +
    'Output is line-numbered so lines can be referenced precisely.',
  parameters: ReadFileParams,
  async execute(args) {
    const { path, start_line, end_line, max_lines } = ReadFileParams.parse(args);

    let content: string;
    try {
      content = await readFile(path, 'utf-8');
    } catch (err: any) {
      return `Error reading file ${path}: ${err.message}`;
    }

    const allLines = content.split('\n');
    const total = allLines.length;

    const start = Math.max(1, start_line ?? 1);
    const end = Math.min(total, end_line ?? total);

    if (start > total) {
      return `File: ${path}\n(start_line ${start} is past the end of the file, which has ${total} lines)`;
    }

    let slice = allLines.slice(start - 1, end);

    if (max_lines !== undefined && slice.length > max_lines) {
      slice = slice.slice(0, max_lines);
    }

    const width = String(end).length;
    const numbered = slice.map((line, i) => {
      const lineNo = String(start + i).padStart(width, ' ');
      return `${lineNo} | ${line}`;
    });

    const header = end_line || start_line || max_lines
      ? `File: ${path} (lines ${start}-${start + slice.length - 1} of ${total})`
      : `File: ${path} (${total} lines)`;

    return `${header}\n\n${numbered.join('\n')}`;
  },
};
