import React from 'react';
import { Box, Text } from 'ink';
import { basename } from 'path';
import { tuiTheme } from './theme';

export type DiffLineKind = 'context' | 'add' | 'remove';

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
}

/**
 * Build a simple add/remove diff from an edit_file's old/new strings.
 *
 * This shows the changed snippet faithfully. Full line-numbered,
 * surrounding-context diffs come later when edit_file emits a real unified
 * patch against the file on disk.
 */
export function buildEditDiff(oldStr: string, newStr: string): DiffLine[] {
  const lines: DiffLine[] = [];
  for (const text of oldStr.split('\n')) lines.push({ kind: 'remove', text });
  for (const text of newStr.split('\n')) lines.push({ kind: 'add', text });
  return lines;
}

const MARK: Record<DiffLineKind, string> = { context: ' ', add: '+', remove: '-' };

function colorFor(kind: DiffLineKind): string {
  if (kind === 'add') return tuiTheme.colors.success;
  if (kind === 'remove') return tuiTheme.colors.danger;
  return tuiTheme.colors.muted;
}

export interface DiffCardProps {
  file: string;
  lines: DiffLine[];
  /** Cap rendered lines so a huge edit doesn't flood the transcript. */
  maxLines?: number;
}

/**
 * Bordered diff card titled with the filename, rendering removed lines red and
 * added lines green (unified-diff style), matching the working-view mockup.
 */
export const DiffCard: React.FC<DiffCardProps> = ({ file, lines, maxLines = 16 }) => {
  const shown = lines.slice(0, maxLines);
  const hidden = lines.length - shown.length;

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor={tuiTheme.colors.border}
      paddingX={1}
      marginY={1}
    >
      <Text color={tuiTheme.colors.muted}>{file ? basename(file) : 'diff'}</Text>
      {shown.map((line, i) => (
        <Text key={i} color={colorFor(line.kind)}>
          {MARK[line.kind]} {line.text}
        </Text>
      ))}
      {hidden > 0 ? (
        <Text color={tuiTheme.colors.subtle} dimColor>
          … {hidden} more line{hidden === 1 ? '' : 's'}
        </Text>
      ) : null}
    </Box>
  );
};
