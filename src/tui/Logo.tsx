import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';

export const Logo: React.FC = () => {
  return (
    <Box flexDirection="column" marginBottom={1}>
      <Text color={tuiTheme.colors.brand} bold>{' _______  _______  ______    _______ '}</Text>
      <Text color={tuiTheme.colors.brand} bold>{'|       ||       ||    _ |  |       |'}</Text>
      <Text color={tuiTheme.colors.brand} bold>{'|____   ||    ___||   | ||  |   _   |'}</Text>
      <Text color={tuiTheme.colors.brand} bold>{' ____|  ||   |___ |   |_||_ |  | |  |'}</Text>
      <Text color={tuiTheme.colors.brand} bold>{'| ______||    ___||    __  ||  |_|  |'}</Text>
      <Text color={tuiTheme.colors.brand} bold>{'| |_____ |   |___ |   |  | ||       |'}</Text>
      <Text color={tuiTheme.colors.brand} bold>{'|_______||_______||___|  |_||_______|'}</Text>

      <Box marginTop={1}>
        <Text color={tuiTheme.colors.muted} dimColor>
          Terminal coding agent for focused repo work
        </Text>
      </Box>
    </Box>
  );
};
