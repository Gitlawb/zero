import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

const HINTS: ReadonlyArray<readonly [key: string, label: string]> = [
  ['Enter', 'send'],
  ['Tab', 'complete'],
  ['Ctrl+C', 'exit'],
];

/**
 * Minimal shortcut hints: one quiet line with the keys emphasized in cyan and
 * a faint middot between groups — no boxed keycaps (they read as heavy/noisy).
 * Wraps on narrow terminals.
 */
export const ShortcutHints: React.FC = () => (
  <Box paddingX={1} flexWrap="wrap" flexShrink={0}>
    {HINTS.map(([key, label], i) => (
      <Box key={key}>
        <Text color={theme.accent} bold>
          {key}
        </Text>
        <Text color={theme.muted}> {label}</Text>
        {i < HINTS.length - 1 && <Text color={theme.label}>{'   ·   '}</Text>}
      </Box>
    ))}
  </Box>
);
