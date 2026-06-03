import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

export interface HeaderProps {
  cwd: string;
  project: string;
  status: string;
  provider: string;
  model: string;
  /** Terminal width; below ~100 cols the bar stacks and the cwd is shortened. */
  width?: number;
}

const Divider: React.FC = () => <Text color={theme.label}>{'  │  '}</Text>;

// Shorten long paths from the middle so the left edge (drive) and the leaf
// (current folder) both stay visible on narrow terminals.
function truncateMiddle(value: string, max: number): string {
  if (value.length <= max) return value;
  const keep = Math.max(4, Math.floor((max - 3) / 2));
  return `${value.slice(0, keep)}...${value.slice(-keep)}`;
}

/**
 * Full-width bordered top bar: identity/cwd/project on the left, status/
 * provider on the right, pinned to the edges via `space-between`. On narrow
 * terminals it stacks vertically and truncates the cwd so nothing overflows.
 */
export const Header: React.FC<HeaderProps> = ({
  cwd,
  project,
  status,
  provider,
  model,
  width = 120,
}) => {
  const compact = width < 100;
  const displayCwd = compact ? truncateMiddle(cwd, Math.max(18, width - 46)) : cwd;

  return (
    <Box
      borderStyle="round"
      borderColor={theme.border}
      paddingX={1}
      flexDirection={compact ? 'column' : 'row'}
      justifyContent="space-between"
      flexShrink={0}
    >
      {/* Left cluster */}
      <Box>
        <Text color={theme.accent} bold>
          ZERO
        </Text>
        <Divider />
        <Text color={theme.label}>cwd: </Text>
        <Text color={theme.value}>{displayCwd}</Text>
        <Divider />
        <Text color={theme.label}>project: </Text>
        <Text color={theme.value}>{project}</Text>
      </Box>

      {/* Right cluster */}
      <Box flexShrink={0}>
        <Text color={theme.label}>status: </Text>
        <Text color={theme.ok} bold>
          {status}
        </Text>
        <Divider />
        <Text color={theme.label}>provider: </Text>
        <Text color={theme.accent}>{provider}</Text>
        <Text color={theme.label}> / </Text>
        <Text color={theme.value}>{model}</Text>
      </Box>
    </Box>
  );
};
