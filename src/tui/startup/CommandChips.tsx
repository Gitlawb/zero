import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

export const DEFAULT_CHIPS = ['/plan', '/debug', '/tools', '/model', '/provider'];

export interface CommandChipsProps {
  chips?: string[];
}

/**
 * Centered row of bordered command "chips". Each chip is its own bordered Box
 * so spacing stays even; `flexWrap` lets them wrap on narrow terminals instead
 * of overflowing.
 */
export const CommandChips: React.FC<CommandChipsProps> = ({ chips = DEFAULT_CHIPS }) => (
  <Box justifyContent="center" flexWrap="wrap">
    {chips.map((chip) => (
      <Box key={chip} borderStyle="round" borderColor={theme.border} paddingX={2} marginX={1}>
        <Text color={theme.accent}>{chip}</Text>
      </Box>
    ))}
  </Box>
);
