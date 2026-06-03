import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';

export const Logo: React.FC = () => {
  return (
    <Box flexDirection="column" marginBottom={1}>
      <Box flexDirection="row">
        <Text color={tuiTheme.colors.brand} backgroundColor={tuiTheme.colors.panel} bold>
          {' ZERO '}
        </Text>
        <Text color={tuiTheme.colors.muted}>
          {'  '}terminal coding agent
        </Text>
      </Box>
      <Box marginTop={1} flexDirection="column">
        <Text color={tuiTheme.colors.subtle}>
          ----------------------------------------
        </Text>
        <Text color={tuiTheme.colors.muted} dimColor>
          codebase context, tools, and model control in one terminal.
        </Text>
      </Box>
    </Box>
  );
};
