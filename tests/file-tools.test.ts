import { describe, it, expect, beforeEach, afterEach } from 'bun:test';
import { mkdtemp, rm, writeFile, readFile } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import { readFileTool } from '../src/tools/read_file';
import { writeFileTool } from '../src/tools/write_file';
import { editFileTool } from '../src/tools/edit_file';

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

  it('returns line-numbered output for a whole file', async () => {
    const file = join(dir, 'lines.txt');
    await writeFile(file, 'one\ntwo\nthree\n', 'utf-8');

    const result = await readFileTool.execute({ path: file });
    expect(result).toContain('1 | one');
    expect(result).toContain('2 | two');
    expect(result).toContain('3 | three');
  });

  it('respects start_line and end_line', async () => {
    const file = join(dir, 'lines.txt');
    await writeFile(file, 'one\ntwo\nthree\nfour\n', 'utf-8');

    const result = await readFileTool.execute({
      path: file,
      start_line: 2,
      end_line: 3,
    });

    expect(result).toContain('2 | two');
    expect(result).toContain('3 | three');
    expect(result).not.toContain('1 | one');
    expect(result).not.toContain('4 | four');
  });

  it('caps the output to max_lines', async () => {
    const file = join(dir, 'lines.txt');
    await writeFile(file, 'a\nb\nc\nd\ne\n', 'utf-8');

    const result = await readFileTool.execute({ path: file, max_lines: 2 });
    expect(result).toContain('1 | a');
    expect(result).toContain('2 | b');
    expect(result).not.toContain('3 | c');
  });

  it('handles start_line past EOF gracefully', async () => {
    const file = join(dir, 'short.txt');
    await writeFile(file, 'one\n', 'utf-8');

    const result = await readFileTool.execute({ path: file, start_line: 99 });
    expect(result).toContain('start_line 99');
    // 'one\n'.split('\n') has 2 entries (the trailing newline yields an empty string),
    // so the message should describe the actual line count of the file.
    expect(result).toMatch(/has \d+ lines/);
  });
});

describe('writeFileTool', () => {
  it('creates a new file', async () => {
    const file = join(dir, 'new.txt');
    const result = await writeFileTool.execute({ path: file, content: 'hello' });
    expect(result).toContain('Created');
    expect(await readFile(file, 'utf-8')).toBe('hello');
  });

  it('refuses to overwrite without explicit permission', async () => {
    const file = join(dir, 'exists.txt');
    await writeFile(file, 'original', 'utf-8');

    const result = await writeFileTool.execute({ path: file, content: 'new' });
    expect(result).toContain('already exists');
    expect(await readFile(file, 'utf-8')).toBe('original');
  });

  it('overwrites when overwrite: true', async () => {
    const file = join(dir, 'exists.txt');
    await writeFile(file, 'original', 'utf-8');

    const result = await writeFileTool.execute({
      path: file,
      content: 'new',
      overwrite: true,
    });
    expect(result).toContain('Overwrote');
    expect(await readFile(file, 'utf-8')).toBe('new');
  });

  it('refuses to overwrite an empty existing file without overwrite: true', async () => {
    // Regression: previously the guard used `existing.length > 0`, so a
    // touched-but-empty file was silently overwritten. The tool must treat
    // any existing path as already present.
    const file = join(dir, 'empty.txt');
    await writeFile(file, '', 'utf-8');
    expect((await readFile(file, 'utf-8')).length).toBe(0);

    const result = await writeFileTool.execute({ path: file, content: 'new' });
    expect(result).toContain('already exists');
    expect(await readFile(file, 'utf-8')).toBe('');
  });

  it('overwrites an empty file when overwrite: true', async () => {
    const file = join(dir, 'empty.txt');
    await writeFile(file, '', 'utf-8');

    const result = await writeFileTool.execute({
      path: file,
      content: 'new',
      overwrite: true,
    });
    expect(result).toContain('Overwrote');
    expect(await readFile(file, 'utf-8')).toBe('new');
  });

  it('creates parent directories as needed', async () => {
    const file = join(dir, 'nested', 'deep', 'file.txt');
    const result = await writeFileTool.execute({ path: file, content: 'hi' });
    expect(result).toContain('Created');
    expect(await readFile(file, 'utf-8')).toBe('hi');
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

  it('rejects edits when old_string is ambiguous (matches multiple places)', async () => {
    const file = join(dir, 'dup.txt');
    await writeFile(file, 'x\nx\n', 'utf-8');

    const result = await editFileTool.execute({
      path: file,
      old_string: 'x',
      new_string: 'y',
    });

    expect(result).toContain('matches 2 locations');
    // File should be untouched
    expect(await readFile(file, 'utf-8')).toBe('x\nx\n');
  });

  it('replaces every occurrence when replace_all is true', async () => {
    const file = join(dir, 'dup.txt');
    await writeFile(file, 'x\nx\n', 'utf-8');

    const result = await editFileTool.execute({
      path: file,
      old_string: 'x',
      new_string: 'y',
      replace_all: true,
    });

    expect(result).toContain('replaced 2 occurrences');
    expect(await readFile(file, 'utf-8')).toBe('y\ny\n');
  });
});
