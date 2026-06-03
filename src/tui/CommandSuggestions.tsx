import React from 'react';
import { Box, Text } from 'ink';
import { listTuiCommands } from './commands';
import { tuiTheme } from './theme';

interface CommandSuggestionsProps {
  suggestions: string[];
  selectedIndex?: number;
  terminalWidth?: number;
}

function truncate(text: string, maxLength: number): string {
  if (maxLength <= 0) return '';
  if (text.length <= maxLength) return text;
  if (maxLength <= 3) return '';
  return `${text.slice(0, maxLength - 3)}...`;
}

export const CommandSuggestions: React.FC<CommandSuggestionsProps> = ({
  suggestions,
  selectedIndex = 0,
  terminalWidth = 80,
}) => {
  if (suggestions.length === 0) return null;

  const commands = listTuiCommands();
  const maxVisible = 6;
  const windowStart = suggestions.length > maxVisible
    ? Math.max(0, Math.min(selectedIndex - 3, suggestions.length - maxVisible))
    : 0;
  const visibleSuggestions = suggestions.slice(windowStart, windowStart + maxVisible);
  const nameWidth = suggestions.reduce((max, suggestion) => {
    const display = suggestion.startsWith('/') ? suggestion.slice(1) : suggestion;
    return Math.max(max, display.length);
  }, 0);
  const descriptionWidth = Math.max(0, terminalWidth - nameWidth - 8);

  return (
    <Box
      flexDirection="column"
      paddingLeft={3}
    >
      {visibleSuggestions.map((suggestion, index) => {
        const realIndex = windowStart + index;
        const isFocused = realIndex === selectedIndex;
        const display = suggestion.startsWith('/') ? suggestion.slice(1) : suggestion;
        const command = commands.find((item) => item.name === suggestion);
        const description = truncate(command?.description ?? '', descriptionWidth);
        const rowColor = isFocused ? tuiTheme.colors.text : tuiTheme.colors.muted;
        const descriptionPadding = ' '.repeat(Math.max(1, nameWidth - display.length + 2));

        return (
          <Box key={suggestion} flexDirection="row">
            <Text color={rowColor} bold={isFocused}>{display}</Text>
            {description ? (
              <Text color={rowColor} bold={isFocused}>
                {descriptionPadding}{description}
              </Text>
            ) : null}
          </Box>
        );
      })}
    </Box>
  );
};
