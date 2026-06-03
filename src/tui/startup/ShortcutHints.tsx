import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

const HINTS = [
  { key: 'Enter', label: 'sends' },
  { key: 'Tab', label: 'accepts command' },
  { key: 'Ctrl+C', label: 'exits' },
];

export const ShortcutHints: React.FC = () => (
  <Box paddingX={1} marginTop={1} flexWrap="wrap" flexShrink={0} alignItems="center">
    {HINTS.map((hint, index) => (
      <Box key={hint.key} marginRight={index === HINTS.length - 1 ? 0 : 3} alignItems="center">
        <Box borderStyle="round" borderColor={theme.label} paddingX={1}>
          <Text color={theme.value}>{hint.key}</Text>
        </Box>
        <Box marginLeft={1}>
          <Text color={theme.muted}>{hint.label}</Text>
        </Box>
      </Box>
    ))}
  </Box>
);
