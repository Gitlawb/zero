import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';
import type { TuiModeState } from './types';

interface TuiPromptBoxProps extends TuiModeState {
  input: string;
  providerName: string;
  modelName: string;
  inputStyle: 'border' | 'solid';
  terminalWidth: number;
}

export const TuiPromptBox: React.FC<TuiPromptBoxProps> = ({
  input,
  isPlanMode,
  isThinking,
  inputStyle,
  terminalWidth,
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

  const prompt = (
    <Box
      borderStyle={inputStyle === 'border' ? 'round' : undefined}
      borderColor={borderColor}
      backgroundColor={inputStyle === 'solid' ? tuiTheme.colors.panel : undefined}
      paddingX={1}
      flexDirection="row"
    >
      <Text color={isPlanMode ? tuiTheme.colors.success : tuiTheme.colors.accent}>{tuiTheme.marks.prompt} </Text>
      {input ? (
        <Text color={tuiTheme.colors.text}>{input}</Text>
      ) : (
        <Text color={tuiTheme.colors.muted} dimColor>{placeholder}</Text>
      )}
      <Text color={tuiTheme.colors.muted}>█</Text>
    </Box>
  );

  if (inputStyle !== 'solid') {
    return <Box flexDirection="column" marginTop={1}>{prompt}</Box>;
  }

  const width = Math.max(1, terminalWidth);
  return (
    <Box flexDirection="column" marginTop={1}>
      <Text color={tuiTheme.colors.panel}>{'▄'.repeat(width)}</Text>
      {prompt}
      <Text color={tuiTheme.colors.panel}>{'▀'.repeat(width)}</Text>
    </Box>
  );
};
