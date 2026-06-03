import { describe, expect, it } from 'bun:test';
import {
  ZERO_AUTO_REVIEW_MARKER,
  buildChecksFromEnv,
  buildReviewMarkdown,
  formatOutcome,
  hasBlockingChecks,
  normalizeOutcome,
  parseChangedFiles,
} from '../scripts/pr-review';

describe('PR review automation helpers', () => {
  it('normalizes GitHub action step outcomes defensively', () => {
    expect(normalizeOutcome('success')).toBe('success');
    expect(normalizeOutcome('timed-out')).toBe('timed_out');
    expect(normalizeOutcome('ACTION_REQUIRED')).toBe('action_required');
    expect(normalizeOutcome(undefined)).toBe('unknown');
  });

  it('builds check rows from workflow environment values', () => {
    const checks = buildChecksFromEnv({
      ZERO_REVIEW_DIFF_CHECK: 'success',
      ZERO_REVIEW_TYPECHECK: 'success',
      ZERO_REVIEW_TEST: 'failure',
      ZERO_REVIEW_BUILD: 'success',
      ZERO_REVIEW_SMOKE: 'skipped',
    });

    expect(checks.map((check) => check.outcome)).toEqual([
      'success',
      'success',
      'failure',
      'success',
      'skipped',
    ]);
    expect(hasBlockingChecks(checks)).toBe(true);
  });

  it('formats an approving deterministic review when checks pass', () => {
    const checks = buildChecksFromEnv({
      ZERO_REVIEW_DIFF_CHECK: 'success',
      ZERO_REVIEW_TYPECHECK: 'success',
      ZERO_REVIEW_TEST: 'success',
      ZERO_REVIEW_BUILD: 'success',
      ZERO_REVIEW_SMOKE: 'success',
    });
    const markdown = buildReviewMarkdown({
      owner: 'Gitlawb',
      repo: 'zero',
      number: 30,
      headSha: 'abcdef1234567890',
      checks,
      changedFiles: ['src/index.ts', 'tests/pr-review.test.ts'],
    });

    expect(markdown).toContain(ZERO_AUTO_REVIEW_MARKER);
    expect(markdown).toContain('Verdict: **No blockers found**');
    expect(markdown).toContain('- None found.');
    expect(markdown).toContain('`src/index.ts`');
  });

  it('formats check failures as blockers', () => {
    const checks = buildChecksFromEnv({
      ZERO_REVIEW_DIFF_CHECK: 'success',
      ZERO_REVIEW_TYPECHECK: 'failure',
      ZERO_REVIEW_TEST: 'success',
      ZERO_REVIEW_BUILD: 'success',
      ZERO_REVIEW_SMOKE: 'success',
    });
    const markdown = buildReviewMarkdown({
      owner: 'Gitlawb',
      repo: 'zero',
      number: 31,
      checks,
    });

    expect(markdown).toContain('Verdict: **Changes requested**');
    expect(markdown).toContain('`bun run typecheck` ended with `failure`');
    expect(formatOutcome('success')).toBe('[pass]');
    expect(formatOutcome('failure')).toBe('[fail]');
  });

  it('parses changed file output into a stable sorted list', () => {
    expect(parseChangedFiles('tests/b.test.ts\n\nsrc/a.ts\r\n')).toEqual([
      'src/a.ts',
      'tests/b.test.ts',
    ]);
  });
});
