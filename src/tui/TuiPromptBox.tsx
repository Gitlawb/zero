import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';
import type { TuiModeState } from './types';

interface TuiPromptBoxProps extends TuiModeState {
  input: string;
  providerName: string;
  modelName: string;
  inputStyle: 'border' | 'solid';
  inputBackground?: string;
  terminalWidth: number;
}

export const TuiPromptBox: React.FC<TuiPromptBoxProps> = ({
  input,
  isPlanMode,
  inputStyle,
  inputBackground,
  terminalWidth,
}) => {
  const borderColor = isPlanMode ? tuiTheme.colors.success : tuiTheme.colors.brand;
  const backgroundColor = inputBackground ?? tuiTheme.colors.panel;

  const prompt = (
    <Box
      borderStyle={inputStyle === 'border' ? 'round' : undefined}
      borderColor={borderColor}
      backgroundColor={inputStyle === 'solid' ? backgroundColor : undefined}
      paddingX={1}
      flexDirection="row"
      alignItems="center"
    >
      <Text color={isPlanMode ? tuiTheme.colors.success : tuiTheme.colors.accent}>{'> '}</Text>
      {input ? (
        <>
          <Text color={tuiTheme.colors.text}>{input}</Text>
          <Text color={tuiTheme.colors.muted}>█</Text>
        </>
      ) : (
        <>
          <Text color={tuiTheme.colors.muted}>█ </Text>
          <Text color={tuiTheme.colors.muted} wrap="truncate">Type your message or @path/to/file</Text>
        </>
      )}
    </Box>
  );

  if (inputStyle !== 'solid') {
    return <Box flexDirection="column">{prompt}</Box>;
  }

  return (
    <Box flexDirection="column">
      <Box width="100%" height={1}>
        <Text color={backgroundColor}>{'▄'.repeat(terminalWidth)}</Text>
      </Box>
      {prompt}
      <Box width="100%" height={1}>
        <Text color={backgroundColor}>{'▀'.repeat(terminalWidth)}</Text>
      </Box>
    </Box>
  );
};
