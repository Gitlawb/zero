import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';
import type { TuiModeState } from './types';

interface TuiPromptBoxProps extends TuiModeState {
  input: string;
  providerName: string;
  modelName: string;
}

export const TuiPromptBox: React.FC<TuiPromptBoxProps> = ({
  input,
  providerName,
  modelName,
  isPlanMode,
  debugMode,
  toolsEnabled,
  isThinking,
}) => {
  const borderColor = isThinking
    ? tuiTheme.colors.warning
    : isPlanMode
      ? tuiTheme.colors.success
      : tuiTheme.colors.border;

  return (
    <Box
      borderStyle="single"
      borderColor={borderColor}
      paddingX={1}
      paddingY={0}
      flexDirection="row"
      justifyContent="space-between"
      alignItems="center"
    >
      <Box flexDirection="row" flexGrow={1}>
        <Text color={isPlanMode ? tuiTheme.colors.success : tuiTheme.colors.accent}>
          {tuiTheme.marks.prompt}{' '}
        </Text>
        <Text color={tuiTheme.colors.text}>{input}</Text>
        <Text color={tuiTheme.colors.muted}>{tuiTheme.marks.cursor}</Text>
      </Box>

      <Box flexDirection="row">
        {debugMode && <Text color={tuiTheme.colors.warning}>debug </Text>}
        {!toolsEnabled && <Text color={tuiTheme.colors.danger}>tools off </Text>}
        <Text color={tuiTheme.colors.brand} bold>{providerName}</Text>
        <Text color={tuiTheme.colors.muted}> / </Text>
        <Text color={tuiTheme.colors.model}>{modelName}</Text>
      </Box>
    </Box>
  );
};
