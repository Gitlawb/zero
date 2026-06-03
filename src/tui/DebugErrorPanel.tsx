import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';

interface DebugErrorPanelProps {
  error: any;
}

export const DebugErrorPanel: React.FC<DebugErrorPanelProps> = ({ error }) => {
  if (!error) return null;

  const stack = typeof error?.stack === 'string'
    ? error.stack.split('\n').slice(0, 8).join('\n')
    : undefined;

  return (
    <Box
      borderStyle="single"
      borderColor={tuiTheme.colors.danger}
      paddingX={1}
      paddingY={0}
      marginBottom={1}
      flexDirection="column"
    >
      <Text color={tuiTheme.colors.danger} bold>Debug error</Text>
      <Text color={tuiTheme.colors.muted} dimColor>
        {error.message || String(error)}
      </Text>
      {stack && (
        <Text color={tuiTheme.colors.muted} dimColor>
          {stack}
        </Text>
      )}
      <Text color={tuiTheme.colors.brand} dimColor>
        Full details were also printed to stderr. Use /debug false to hide this panel.
      </Text>
    </Box>
  );
};
