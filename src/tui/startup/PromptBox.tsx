import React, { useEffect, useState } from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

export interface PromptBoxProps {
  /** Current typed value; empty shows the placeholder. Controlled by the shell. */
  value: string;
  placeholder: string;
}

/**
 * Full-width bottom input box: the `zero >` sigil, then the typed value or a
 * muted placeholder, then a blinking block cursor. Presentational — the parent
 * owns keystrokes and passes `value` in.
 */
export const PromptBox: React.FC<PromptBoxProps> = ({ value, placeholder }) => {
  const [cursorOn, setCursorOn] = useState(true);
  useEffect(() => {
    const timer = setInterval(() => setCursorOn((on) => !on), 500);
    return () => clearInterval(timer);
  }, []);

  const isEmpty = value.length === 0;

  return (
    <Box borderStyle="round" borderColor={theme.border} paddingX={1} flexShrink={0}>
      <Text color={theme.accent} bold>
        zero{' > '}
      </Text>
      {isEmpty ? (
        <Text color={theme.muted}>{placeholder}</Text>
      ) : (
        <Text color={theme.value}>{value}</Text>
      )}
      {/* Render a space when "off" so the cursor blink doesn't shift width. */}
      <Text color={theme.accent}>{cursorOn ? '█' : ' '}</Text>
    </Box>
  );
};
