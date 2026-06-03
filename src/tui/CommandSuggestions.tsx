import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';

interface CommandSuggestionsProps {
  suggestions: string[];
}

export const CommandSuggestions: React.FC<CommandSuggestionsProps> = ({ suggestions }) => {
  if (suggestions.length === 0) return null;

  return (
    <Box
      paddingX={1}
      marginTop={1}
      marginBottom={1}
      flexDirection="row"
    >
      <Text color={tuiTheme.colors.accent} bold>commands </Text>
      <Text>
        {suggestions.map((suggestion, index) => (
          <Text
            key={suggestion}
            color={index === 0 ? tuiTheme.colors.brand : tuiTheme.colors.muted}
          >
            {suggestion}{index < suggestions.length - 1 ? '  ' : ''}
          </Text>
        ))}
        <Text color={tuiTheme.colors.muted} dimColor>
          {' '}Tab accepts first match
        </Text>
      </Text>
    </Box>
  );
};
