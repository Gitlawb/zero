import React from 'react';
import { Box, Text } from 'ink';
import type { ToolApprovalRequest } from '../agent/loop';
import { tuiTheme } from './theme';

interface ToolApprovalPanelProps {
  request: ToolApprovalRequest;
}

const LABELS: Record<string, string> = {
  bash: 'Bash',
  write_file: 'Write file',
  edit_file: 'Edit file',
  apply_patch: 'Apply patch',
};

export const ToolApprovalPanel: React.FC<ToolApprovalPanelProps> = ({ request }) => {
  const args = normalizeArgs(request.parsedArgs);
  const preview = buildPreview(request.toolCall.name, args);

  return (
    <Box
      borderStyle="single"
      borderColor={tuiTheme.colors.warning}
      flexDirection="column"
      paddingX={1}
      marginTop={1}
    >
      <Box flexDirection="row">
        <Text color={tuiTheme.colors.warning} bold>
          Allow {LABELS[request.toolCall.name] || request.toolCall.name}?
        </Text>
        <Text color={tuiTheme.colors.muted}> {request.safety.sideEffect}</Text>
      </Box>

      <Text color={tuiTheme.colors.muted}>{request.reason}</Text>

      {preview.map((line) => (
        <Text key={line} color={tuiTheme.colors.text}>
          {line}
        </Text>
      ))}

      <Box marginTop={1} flexDirection="row">
        <Text color={tuiTheme.colors.success} bold>[y]</Text>
        <Text color={tuiTheme.colors.muted}> allow  </Text>
        <Text color={tuiTheme.colors.danger} bold>[n]</Text>
        <Text color={tuiTheme.colors.muted}> deny  </Text>
        <Text color={tuiTheme.colors.brand} bold>[a]</Text>
        <Text color={tuiTheme.colors.muted}> always this session</Text>
      </Box>
    </Box>
  );
};

function normalizeArgs(value: unknown): Record<string, any> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, any>;
  }
  return {};
}

function buildPreview(toolName: string, args: Record<string, any>): string[] {
  if (toolName === 'bash') {
    return [
      `command: ${String(args.command ?? '')}`,
      `cwd: ${String(args.cwd ?? process.cwd())}`,
    ];
  }

  if (toolName === 'write_file') {
    const content = typeof args.content === 'string' ? args.content : '';
    return [
      `path: ${String(args.path ?? '')}`,
      `content: ${content.length} bytes`,
      `overwrite: ${args.overwrite === true ? 'yes' : 'no'}`,
    ];
  }

  if (toolName === 'edit_file') {
    return [
      `path: ${String(args.path ?? '')}`,
      `change: -${lineCount(args.old_string)} +${lineCount(args.new_string)}`,
      `replace: ${args.replace_all ? 'all matches' : 'one match'}`,
    ];
  }

  if (toolName === 'apply_patch') {
    const patch = typeof args.patch === 'string' ? args.patch : '';
    const files = summarizePatchFiles(patch);
    const diff = summarizePatchLines(patch);
    return [
      `files: ${files.length > 0 ? files.join(', ') : 'unknown'}`,
      `diff: +${diff.added} -${diff.removed}`,
    ];
  }

  return [`args: ${JSON.stringify(args).slice(0, 160)}`];
}

function lineCount(value: unknown): number {
  return typeof value === 'string' && value.length > 0 ? value.split('\n').length : 0;
}

function summarizePatchFiles(patch: string): string[] {
  const files = new Set<string>();
  for (const line of patch.split('\n')) {
    const diffMatch = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
    if (diffMatch?.[2]) {
      files.add(diffMatch[2]);
      continue;
    }

    const fileMatch = line.match(/^\+\+\+ (?:b\/)?(.+)$/);
    if (fileMatch?.[1] && fileMatch[1] !== '/dev/null') {
      files.add(fileMatch[1]);
    }
  }

  return Array.from(files).slice(0, 4);
}

function summarizePatchLines(patch: string): { added: number; removed: number } {
  let added = 0;
  let removed = 0;

  for (const line of patch.split('\n')) {
    if (line.startsWith('+++') || line.startsWith('---')) continue;
    if (line.startsWith('+')) added++;
    if (line.startsWith('-')) removed++;
  }

  return { added, removed };
}
