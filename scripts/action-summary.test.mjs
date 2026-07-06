import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { summarizeOutput } from './action-summary.mjs';

const scriptPath = fileURLToPath(new URL('./action-summary.mjs', import.meta.url));
const repoRoot = fileURLToPath(new URL('..', import.meta.url));

function jsonl(events) {
  return events.map((event) => JSON.stringify(event)).join('\n');
}

test('stream-json final followed by run_end returns final text', () => {
  const output = jsonl([
    { type: 'final', text: 'Done. Tests passed.' },
    { type: 'run_end', status: 'success', exitCode: 0 },
  ]);

  assert.equal(summarizeOutput('stream-json', output), 'Done. Tests passed.');
});

test('stream-json error followed by run_end returns error message', () => {
  const output = jsonl([
    { type: 'error', code: 'provider_error', message: 'provider request failed' },
    { type: 'run_end', status: 'error', exitCode: 3 },
  ]);

  assert.equal(summarizeOutput('stream-json', output), 'provider request failed');
});

test('stream-json final then error returns error message', () => {
  const output = jsonl([
    { type: 'final', text: 'partial answer' },
    { type: 'error', code: 'incomplete', message: 'run stopped with work unfinished' },
    { type: 'run_end', status: 'incomplete', exitCode: 4 },
  ]);

  assert.equal(summarizeOutput('stream-json', output), 'run stopped with work unfinished');
});

test('json final followed by done returns final text', () => {
  const output = jsonl([
    { type: 'final', text: 'Review complete.' },
    { type: 'done', exit_code: 0 },
  ]);

  assert.equal(summarizeOutput('json', output), 'Review complete.');
});

test('json error followed by done returns error message', () => {
  const output = jsonl([
    { type: 'error', code: 'exec_failed', message: 'command failed' },
    { type: 'done', exit_code: 1 },
  ]);

  assert.equal(summarizeOutput('json', output), 'command failed');
});

test('json final then error returns error message', () => {
  const output = jsonl([
    { type: 'final', text: 'partial answer' },
    { type: 'error', code: 'incomplete', message: 'run stopped with work unfinished' },
    { type: 'done', exit_code: 4 },
  ]);

  assert.equal(summarizeOutput('json', output), 'run stopped with work unfinished');
});

test('text output returns last non-empty line', () => {
  const output = '\nfirst line\n\n  final streamed line  \n';

  assert.equal(summarizeOutput('text', output), '  final streamed line  ');
});

test('unknown format falls back to text behavior', () => {
  const output = 'alpha\n  beta  \n';

  assert.equal(summarizeOutput('xml', output), '  beta  ');
});

test('malformed structured output falls back safely', () => {
  const output = '{not-json}\nraw fallback\n';

  assert.equal(summarizeOutput('stream-json', output), 'raw fallback');
});

test('long multiline final becomes one-line 280-char summary', () => {
  const text = `first line\n${'x'.repeat(400)}`;
  const summary = summarizeOutput('stream-json', jsonl([{ type: 'final', text }]));

  assert.equal(summary.length, 280);
  assert.equal(summary.includes('\n'), false);
  assert.equal(summary, `first line ${'x'.repeat(400)}`.slice(0, 280));
});

test('cli reads output file and prints summary', () => {
  const dir = mkdtempSync(join(tmpdir(), 'zero-action-summary-'));
  try {
    const outputFile = join(dir, 'zero-output.jsonl');
    writeFileSync(
      outputFile,
      jsonl([
        { type: 'final', text: 'CLI summary' },
        { type: 'run_end', status: 'success', exitCode: 0 },
      ]),
    );

    const result = spawnSync(process.execPath, [scriptPath, 'stream-json', outputFile], {
      encoding: 'utf8',
    });

    assert.equal(result.status, 0);
    assert.equal(result.stderr, '');
    assert.equal(result.stdout, 'CLI summary\n');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('cli runs when invoked with relative script path', () => {
  const dir = mkdtempSync(join(tmpdir(), 'zero-action-summary-'));
  try {
    const outputFile = join(dir, 'zero-output.jsonl');
    writeFileSync(
      outputFile,
      jsonl([
        { type: 'final', text: 'relative CLI summary' },
        { type: 'run_end', status: 'success', exitCode: 0 },
      ]),
    );

    const result = spawnSync(process.execPath, ['scripts/action-summary.mjs', 'stream-json', outputFile], {
      cwd: repoRoot,
      encoding: 'utf8',
    });

    assert.equal(result.status, 0);
    assert.equal(result.stderr, '');
    assert.equal(result.stdout, 'relative CLI summary\n');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
