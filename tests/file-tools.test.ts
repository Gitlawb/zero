import { describe, it, expect, beforeEach, afterEach } from 'bun:test';
import { mkdtemp, rm, writeFile, readFile } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import { readFileTool } from '../src/tools/read-file';
import { editFileTool } from '../src/tools/edit-file';

let dir: string;

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'zero-test-'));
});

afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

describe('readFileTool', () => {
  it('returns the file contents', async () => {
    const file = join(dir, 'hello.txt');
    await writeFile(file, 'hello world', 'utf-8');

    const result = await readFileTool.execute({ path: file });
    expect(result).toContain('hello world');
    expect(result).toContain(file);
  });

  it('returns an error message for a missing file', async () => {
    const result = await readFileTool.execute({ path: join(dir, 'nope.txt') });
    expect(result).toContain('Error reading file');
  });
});

describe('editFileTool', () => {
  it('replaces an exact string and writes it back', async () => {
    const file = join(dir, 'code.ts');
    await writeFile(file, 'const a = 1;\nconst b = 2;\n', 'utf-8');

    const result = await editFileTool.execute({
      path: file,
      old_string: 'const a = 1;',
      new_string: 'const a = 42;',
    });

    expect(result).toContain('Successfully edited');
    expect(await readFile(file, 'utf-8')).toBe('const a = 42;\nconst b = 2;\n');
  });

  it('reports when the target string is not found', async () => {
    const file = join(dir, 'code.ts');
    await writeFile(file, 'const a = 1;\n', 'utf-8');

    const result = await editFileTool.execute({
      path: file,
      old_string: 'does not exist',
      new_string: 'whatever',
    });

    expect(result).toContain('Could not find the exact string');
  });

  it('only replaces the first occurrence', async () => {
    const file = join(dir, 'dup.txt');
    await writeFile(file, 'x\nx\n', 'utf-8');

    await editFileTool.execute({ path: file, old_string: 'x', new_string: 'y' });
    expect(await readFile(file, 'utf-8')).toBe('y\nx\n');
  });
});
