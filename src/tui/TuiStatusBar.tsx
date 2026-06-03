import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';
import { LiveDot } from './LiveDot';
import type { TuiModeState } from './types';

interface TuiStatusBarProps extends TuiModeState {
  scrollOffset: number;
  messageCount: number;
  canScrollUp: boolean;
  canScrollDown: boolean;
  modelName?: string;
  totalTokens?: number;
  costUsd?: number;
  contextPercent?: number;
}

export const TuiStatusBar: React.FC<TuiStatusBarProps> = ({
  modelName = 'unknown',
  isThinking,
}) => {
  const isMissingProvider = modelName.toLowerCase().includes('no provider');

  return (
    <Box paddingX={1} flexDirection="row" justifyContent="space-between" marginTop={1}>
      <Box flexDirection="row">
        <Text color={isMissingProvider ? tuiTheme.colors.warning : tuiTheme.colors.muted}>
          {isMissingProvider ? modelName : `${modelName} Model`}
        </Text>
      </Box>

      <Box flexDirection="row">
        {/* Pulses while the agent is working; steady green otherwise. */}
        <LiveDot pulsing={!!isThinking} color={tuiTheme.colors.success} />
        <Text color={tuiTheme.colors.success}> live</Text>
      </Box>
    </Box>
  );
};
