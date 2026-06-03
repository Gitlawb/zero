import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';

interface CommandSuggestionsProps {
  suggestions: string[];
}

export const CommandSuggestions: React.FC<CommandSuggestionsProps> = ({ suggestions }) => {
  if (suggestions.length === 0) return null;

  return (
    <Box paddingX={2} paddingBottom={0}>
      <Text color={tuiTheme.colors.muted} dimColor>
        Suggestions:{' '}
        {suggestions.map((suggestion, index) => (
          <Text key={suggestion} color={index === 0 ? tuiTheme.colors.brand : tuiTheme.colors.muted}>
            {suggestion}{index < suggestions.length - 1 ? '  ' : ''}
          </Text>
        ))}
        {' '}Tab accepts
      </Text>
    </Box>
  );
};
