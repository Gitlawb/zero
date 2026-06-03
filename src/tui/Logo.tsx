import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

export const Logo: React.FC = () => {
  const lines = [
    '   ____  ___  ____  ____ ',
    '  /_  / / _ \\/ __ \\/ __ \\',
    '   / /_/  __/ /_/ / /_/ /',
    '  /___/\\___/\\____/\\____/ ',
  ];
  return (
    <Box flexDirection="column" marginBottom={1}>
      {lines.map((line, index) => (
        <Text key={line} color={index === 1 ? theme.text.accent : theme.ui.active} bold wrap="truncate">
          {line}
        </Text>
      ))}
      <Box flexDirection="row">
        <Text color={theme.ui.comment}>  terminal agent </Text>
        <Text color={theme.text.accent}>▌</Text>
      </Box>
    </Box>
  );
};
