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
      : tuiTheme.colors.brand;
  const placeholder = isThinking
    ? 'Zero is working...'
    : isPlanMode
      ? 'Plan the next change...'
      : 'Ask Zero to inspect, edit, explain, or run a command...';

  return (
    <Box flexDirection="column" marginTop={1} paddingX={1}>
      <Box flexDirection="row">
        <Text color={isPlanMode ? tuiTheme.colors.success : tuiTheme.colors.brand} bold>
          zero {tuiTheme.marks.prompt}{' '}
        </Text>
        {input ? (
          <Text color={tuiTheme.colors.text}>{input}</Text>
        ) : (
          <Text color={tuiTheme.colors.muted} dimColor>{placeholder}</Text>
        )}
        <Text backgroundColor={borderColor} color={borderColor}>{tuiTheme.marks.cursor}</Text>
      </Box>

      <Box flexDirection="row" justifyContent="space-between">
        <Text color={toolsEnabled ? tuiTheme.colors.success : tuiTheme.colors.danger} dimColor>
          perms {toolsEnabled ? 'ask' : 'tools-off'}
        </Text>
        <Box flexDirection="row">
          {debugMode && <Text color={tuiTheme.colors.warning}>debug </Text>}
          {!toolsEnabled && <Text color={tuiTheme.colors.danger}>tools off </Text>}
          <Text color={tuiTheme.colors.muted} dimColor>{providerName} / </Text>
          <Text color={tuiTheme.colors.model} dimColor>{modelName}</Text>
        </Box>
      </Box>
    </Box>
  );
};
