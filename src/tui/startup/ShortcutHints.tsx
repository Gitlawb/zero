import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

const HINTS: ReadonlyArray<readonly [key: string, label: string]> = [
  ['Enter', 'sends'],
  ['Tab', 'accepts command'],
  ['Ctrl+C', 'exits'],
];

/**
 * Keycap-style shortcut hints beneath the prompt. Each key sits in a small
 * bordered box followed by a muted label; `alignItems="center"` keeps the
 * single-line labels aligned against the keycaps. Wraps on narrow terminals.
 */
export const ShortcutHints: React.FC = () => (
  <Box paddingX={1} flexWrap="wrap" alignItems="center" flexShrink={0}>
    {HINTS.map(([key, label]) => (
      <Box key={key} marginRight={3} alignItems="center">
        <Box borderStyle="single" borderColor={theme.label} paddingX={1}>
          <Text color={theme.value}>{key}</Text>
        </Box>
        <Text color={theme.muted}> {label}</Text>
      </Box>
    ))}
  </Box>
);
