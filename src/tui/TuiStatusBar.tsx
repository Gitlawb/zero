import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';
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
  isPlanMode,
}) => {
  const isMissingProvider = modelName.toLowerCase().includes('no provider');

  return (
    <Box paddingX={1} flexDirection="row">
      <Text color={isMissingProvider ? tuiTheme.colors.warning : tuiTheme.colors.muted}>
        {isMissingProvider ? modelName : `${modelName} Model`}
      </Text>
      {isPlanMode && (
        <Text color={tuiTheme.colors.success}> · PLAN MODE</Text>
      )}
    </Box>
  );
};
