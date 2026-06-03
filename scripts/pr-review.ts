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

export async function resolvePullRequestContext(
  env: Record<string, string | undefined>
): Promise<PullRequestContext> {
  const eventPath = env.GITHUB_EVENT_PATH;
  const repository = env.GITHUB_REPOSITORY;
  if (!eventPath) {
    throw new Error('GITHUB_EVENT_PATH is required.');
  }
  if (!repository || !repository.includes('/')) {
    throw new Error('GITHUB_REPOSITORY must be in owner/repo form.');
  }

  const [owner, repo] = repository.split('/') as [string, string];
  const event = await Bun.file(eventPath).json() as {
    pull_request?: {
      number?: number;
      title?: string;
      head?: { sha?: string };
      base?: { ref?: string };
    };
  };
  const pullRequest = event.pull_request;
  if (!pullRequest?.number) {
    throw new Error('This review script must run from a pull_request event.');
  }

  return {
    owner,
    repo,
    number: pullRequest.number,
    title: pullRequest.title,
    headSha: pullRequest.head?.sha ?? env.GITHUB_SHA,
    baseRef: pullRequest.base?.ref,
  };
}

export async function collectChangedFiles(baseRef: string | undefined): Promise<string[]> {
  if (!baseRef) return [];

  const fetchResult = await runGit(['fetch', '--no-tags', '--depth=1', 'origin', baseRef]);
  if (fetchResult.exitCode !== 0) return [];

  const diffResult = await runGit(['diff', '--name-only', `origin/${baseRef}...HEAD`]);
  if (diffResult.exitCode !== 0) return [];

  return diffResult.stdout
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .sort();
}

export async function upsertReviewComment(options: {
  context: PullRequestContext;
  body: string;
  token: string;
}): Promise<void> {
  const apiBase = `https://api.github.com/repos/${options.context.owner}/${options.context.repo}`;
  const headers = {
    Authorization: `Bearer ${options.token}`,
    Accept: 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28',
  };
  const commentsUrl = `${apiBase}/issues/${options.context.number}/comments?per_page=100`;
  const commentsResponse = await fetch(commentsUrl, { headers });
  if (!commentsResponse.ok) {
    throw new Error(`Failed to list PR comments: ${commentsResponse.status} ${commentsResponse.statusText}`);
  }

  const comments = await commentsResponse.json() as Array<{ id: number; body?: string; url: string }>;
  const existing = comments.find((comment) => comment.body?.includes(ZERO_AUTO_REVIEW_MARKER));
  const response = await fetch(existing?.url ?? commentsUrl, {
    method: existing ? 'PATCH' : 'POST',
    headers: {
      ...headers,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ body: options.body }),
  });

  if (!response.ok) {
    throw new Error(`Failed to ${existing ? 'update' : 'create'} PR review comment: ${response.status} ${response.statusText}`);
  }
}

async function runGit(args: string[]): Promise<{ exitCode: number; stdout: string }> {
  const child = Bun.spawn(['git', ...args], {
    stderr: 'pipe',
    stdout: 'pipe',
  });
  const [exitCode, stdout] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
  ]);
  return { exitCode, stdout };
}

function formatChangedFiles(files: string[]): string {
  const visible = files.slice(0, 12);
  const suffix = files.length > visible.length ? `, and ${files.length - visible.length} more` : '';
  return `${visible.map((file) => `\`${file}\``).join(', ')}${suffix}`;
}

async function main(): Promise<void> {
  const context = await resolvePullRequestContext(Bun.env);
  const checks = buildChecksFromEnv(Bun.env);
  const changedFiles = await collectChangedFiles(context.baseRef);
  const body = buildReviewMarkdown({
    ...context,
    checks,
    changedFiles,
  });

  if (Bun.env.ZERO_PR_REVIEW_DRY_RUN === '1') {
    console.log(body);
    return;
  }

  const token = Bun.env.GITHUB_TOKEN;
  if (!token) {
    console.log(body);
    console.warn('GITHUB_TOKEN is missing; printed review summary instead of posting.');
    return;
  }

  try {
    await upsertReviewComment({ context, body, token });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.warn(`Unable to post automated review comment: ${message}`);
    console.log(body);
  }
}

if (import.meta.main) {
  await main();
}
