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
      backgroundColor={tuiTheme.colors.panelAlt}
    >
      <Text color={tuiTheme.colors.accent} backgroundColor={tuiTheme.colors.panelAlt} bold>COMMANDS </Text>
      <Text>
        {suggestions.map((suggestion, index) => (
          <Text
            key={suggestion}
            color={index === 0 ? tuiTheme.colors.brand : tuiTheme.colors.muted}
            backgroundColor={tuiTheme.colors.panelAlt}
          >
            [{suggestion}]{index < suggestions.length - 1 ? ' ' : ''}
          </Text>
        ))}
        <Text color={tuiTheme.colors.muted} backgroundColor={tuiTheme.colors.panelAlt} dimColor>
          {' '}Tab accepts first match
        </Text>
      </Text>
    </Box>
  );
};
