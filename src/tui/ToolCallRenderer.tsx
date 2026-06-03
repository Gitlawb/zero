import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';
import { DiffCard, buildEditDiff } from './DiffCard';
import Spinner from 'ink-spinner';

interface ToolCallRendererProps {
  name: string;
  args: string;
  result?: string;
  status?: 'running' | 'success' | 'error';
}

const LABELS: Record<string, string> = {
  read_file: 'Read',
  list_directory: 'List',
  grep: 'Grep',
  glob: 'Glob',
  bash: 'Bash',
  edit_file: 'Edit',
  write_file: 'Write',
  apply_patch: 'Patch',
  update_plan: 'Plan',
};

function parseArgs(raw: string): any {
  try {
    return JSON.parse(raw || '{}');
  } catch {
    return {};
  }
}

function leaf(p: string): string {
  if (!p) return '';
  const parts = p.split(/[\\/]/);
  return parts[parts.length - 1] || p;
}

// One-line, tool-specific summary:
//   read   → `dispatch.ts (312 lines)`
//   grep   → `"handleDispatch" — 4 matches in 2 files`
//   edit   → `dispatch.ts +3 -2`
function summarize(name: string, args: any, result?: string): string {
  switch (name) {
    case 'read_file': {
      const lines = result ? result.split('\n').length : undefined;
      return `${leaf(args.path)}${lines ? ` (${lines} lines)` : ''}`;
    }
    case 'list_directory':
      return args.path || '.';
    case 'grep':
    case 'glob': {
      const hits =
        result && !/^no matches/i.test(result.trim())
          ? result.trim().split('\n').filter(Boolean)
          : [];
      const files = new Set(hits.map((h) => h.split(':')[0])).size;
      const n = hits.length;
      const where = files ? ` in ${files} file${files === 1 ? '' : 's'}` : '';
      return `"${args.pattern || args.glob || ''}" — ${n} match${n === 1 ? '' : 'es'}${where}`;
    }
    case 'bash':
      return args.command || '';
    case 'edit_file':
    case 'apply_patch': {
      const add = typeof args.new_string === 'string' ? args.new_string.split('\n').length : 0;
      const rem = typeof args.old_string === 'string' ? args.old_string.split('\n').length : 0;
      return `${leaf(args.path)} +${add} -${rem}`;
    }
    case 'write_file':
      return leaf(args.path);
    case 'update_plan':
      return 'updated plan';
    default:
      return '';
  }
}

/**
 * A single activity row: status dot + tool label + one-line summary. For edits
 * it also renders an inline DiffCard of the changed snippet — matching the
 * working-view mockup (collapsed, scannable rows + a diff for changes).
 */
export const ToolCallRenderer: React.FC<ToolCallRendererProps> = ({
  name,
  args,
  result,
  status = 'success',
}) => {
  const parsed = parseArgs(args);
  const label = LABELS[name] || name;
  const summary = summarize(name, parsed, result);
  const dotColor =
    status === 'running'
      ? tuiTheme.colors.warning
      : status === 'error'
        ? tuiTheme.colors.danger
        : tuiTheme.colors.success;

  const showDiff =
    (name === 'edit_file' || name === 'apply_patch') &&
    typeof parsed.old_string === 'string' &&
    typeof parsed.new_string === 'string';

  return (
    <Box flexDirection="column">
      <Box flexDirection="row" paddingX={1}>
        {/* Animated spinner while running; resolves to a static dot when done. */}
        {status === 'running' ? (
          <Text color={tuiTheme.colors.warning}>
            <Spinner type="dots" />{' '}
          </Text>
        ) : (
          <Text color={dotColor}>● </Text>
        )}
        <Text color={tuiTheme.colors.brand} bold>
          {label}{' '}
        </Text>
        {summary ? <Text color={tuiTheme.colors.muted}>{summary}</Text> : null}
      </Box>
      {showDiff ? (
        <Box paddingX={1}>
          <DiffCard file={parsed.path || ''} lines={buildEditDiff(parsed.old_string, parsed.new_string)} />
        </Box>
      ) : null}
    </Box>
  );
};
