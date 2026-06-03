import React from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';
import type { TuiModeState } from './types';

interface TuiStatusBarProps extends TuiModeState {
  scrollOffset: number;
  messageCount: number;
  canScrollUp: boolean;
  canScrollDown: boolean;
}

export const TuiStatusBar: React.FC<TuiStatusBarProps> = ({
  scrollOffset,
  messageCount,
  canScrollUp,
  canScrollDown,
  isPlanMode,
  debugMode,
  toolsEnabled,
  isThinking,
}) => {
  const flags = [
    isThinking ? 'running' : undefined,
    isPlanMode ? 'plan' : undefined,
    debugMode ? 'debug' : undefined,
    toolsEnabled ? undefined : 'tools off',
  ].filter(Boolean);

  return (
    <Box paddingX={1} flexDirection="row" justifyContent="space-between" marginTop={1}>
      <Text color={tuiTheme.colors.muted} dimColor>
        /help  /model  /provider  /plan  /tools
      </Text>
      <Text color={tuiTheme.colors.muted} dimColor>
        {canScrollUp || canScrollDown ? `history ${scrollOffset + 1}/${messageCount}` : 'latest'}
        {flags.length > 0 ? `  ${flags.join(' / ')}` : ''}
      </Text>
    </Box>
  );
};
