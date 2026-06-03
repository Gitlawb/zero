export const ZERO_AUTO_REVIEW_MARKER = '<!-- zero-auto-review -->';

export type ReviewOutcome =
  | 'success'
  | 'failure'
  | 'cancelled'
  | 'skipped'
  | 'timed_out'
  | 'action_required'
  | 'neutral'
  | 'unknown';

export interface ReviewCheck {
  label: string;
  command: string;
  outcome: ReviewOutcome;
}

export interface PullRequestContext {
  owner: string;
  repo: string;
  number: number;
  title?: string;
  headSha?: string;
  baseRef?: string;
}

export interface ReviewSummaryInput extends PullRequestContext {
  checks: ReviewCheck[];
  changedFiles?: string[];
}

const REVIEW_CHECKS: Array<{ env: string; label: string; command: string }> = [
  { env: 'ZERO_REVIEW_DIFF_CHECK', label: 'Diff hygiene', command: 'git diff --check' },
  { env: 'ZERO_REVIEW_TYPECHECK', label: 'Typecheck', command: 'bun run typecheck' },
  { env: 'ZERO_REVIEW_TEST', label: 'Tests', command: 'bun run test' },
  { env: 'ZERO_REVIEW_BUILD', label: 'Build', command: 'bun run build' },
  { env: 'ZERO_REVIEW_SMOKE', label: 'Smoke build', command: 'bun run smoke:build' },
];

const BLOCKING_OUTCOMES = new Set<ReviewOutcome>([
  'failure',
  'cancelled',
  'timed_out',
  'action_required',
  'unknown',
]);

export function normalizeOutcome(value: string | undefined): ReviewOutcome {
  const normalized = (value ?? '').trim().toLowerCase().replace(/-/g, '_');
  if (
    normalized === 'success' ||
    normalized === 'failure' ||
    normalized === 'cancelled' ||
    normalized === 'skipped' ||
    normalized === 'timed_out' ||
    normalized === 'action_required' ||
    normalized === 'neutral'
  ) {
    return normalized;
  }
  return 'unknown';
}

export function buildChecksFromEnv(env: Record<string, string | undefined>): ReviewCheck[] {
  return REVIEW_CHECKS.map((check) => ({
    label: check.label,
    command: check.command,
    outcome: normalizeOutcome(env[check.env]),
  }));
}

export function hasBlockingChecks(checks: readonly ReviewCheck[]): boolean {
  return checks.some((check) => BLOCKING_OUTCOMES.has(check.outcome));
}

export function buildReviewMarkdown(input: ReviewSummaryInput): string {
  const blockers = input.checks.filter((check) => BLOCKING_OUTCOMES.has(check.outcome));
  const changedFiles = input.changedFiles ?? [];
  const headLine = input.headSha ? `Head: \`${input.headSha.slice(0, 12)}\`` : undefined;

  return [
    ZERO_AUTO_REVIEW_MARKER,
    '## Zero automated PR review',
    '',
    `Verdict: **${blockers.length > 0 ? 'Changes requested' : 'No blockers found'}**`,
    '',
    '### Blockers',
    '',
    blockers.length > 0
      ? blockers
          .map((check) => `- \`${check.command}\` ended with \`${check.outcome}\`.`)
          .join('\n')
      : '- None found.',
    '',
    '### Validation',
    '',
    ...input.checks.map(
      (check) => `- ${formatOutcome(check.outcome)} ${check.label}: \`${check.command}\``
    ),
    '',
    '### Scope',
    '',
    headLine ?? `PR: #${input.number}`,
    changedFiles.length > 0
      ? `Changed files (${changedFiles.length}): ${formatChangedFiles(changedFiles)}`
      : 'Changed files: unavailable in this run.',
    '',
    'This deterministic review checks validation status and basic diff hygiene. A human reviewer still owns product judgment and design quality.',
  ].join('\n');
}

export function formatOutcome(outcome: ReviewOutcome): string {
  if (outcome === 'success') return '[pass]';
  if (outcome === 'skipped' || outcome === 'neutral') return '[info]';
  return '[fail]';
}

export function parseChangedFiles(value: string | undefined): string[] {
  return (value ?? '')
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .sort();
}

function formatChangedFiles(files: string[]): string {
  const visible = files.slice(0, 12);
  const suffix = files.length > visible.length ? `, and ${files.length - visible.length} more` : '';
  return `${visible.map((file) => `\`${file}\``).join(', ')}${suffix}`;
}

function resolveLocalContext(env: Record<string, string | undefined>): PullRequestContext {
  const repository = env.GITHUB_REPOSITORY ?? 'Gitlawb/zero';
  const [owner = 'Gitlawb', repo = 'zero'] = repository.split('/');
  return {
    owner,
    repo,
    number: Number(env.ZERO_PR_NUMBER ?? env.GITHUB_REF_NAME?.split('/')[0] ?? 0),
    headSha: env.GITHUB_SHA,
    baseRef: env.GITHUB_BASE_REF,
  };
}

function main(): void {
  const context = resolveLocalContext(Bun.env);
  const checks = buildChecksFromEnv(Bun.env);
  const changedFiles = parseChangedFiles(Bun.env.ZERO_CHANGED_FILES);
  const body = buildReviewMarkdown({
    ...context,
    checks,
    changedFiles,
  });

  console.log(body);
}

if (import.meta.main) {
  main();
}
