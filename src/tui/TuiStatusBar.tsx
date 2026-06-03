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

const Dot: React.FC = () => <Text color={tuiTheme.colors.subtle}>{'  ·  '}</Text>;

/**
 * Footer status bar: model · tokens · cost · context% on the left, and a
 * live/idle indicator on the right. (Usage figures are estimated until the
 * agent loop emits real token usage.)
 */
export const TuiStatusBar: React.FC<TuiStatusBarProps> = ({
  modelName = 'unknown',
  totalTokens = 0,
  costUsd = 0,
  contextPercent = 0,
  isThinking,
}) => {
  return (
    <Box paddingX={1} flexDirection="row" justifyContent="space-between" marginTop={1}>
      <Box flexDirection="row">
        <Text color={tuiTheme.colors.model}>{modelName}</Text>
        <Dot />
        <Text color={tuiTheme.colors.muted}>{totalTokens.toLocaleString('en-US')} tokens</Text>
        <Dot />
        <Text color={tuiTheme.colors.muted}>${costUsd.toFixed(4)}</Text>
        <Dot />
        <Text color={tuiTheme.colors.muted}>ctx {contextPercent}%</Text>
      </Box>

      <Box flexDirection="row">
        {/* Pulses while the agent is working; steady green otherwise. */}
        <LiveDot pulsing={!!isThinking} color={tuiTheme.colors.success} />
        <Text color={tuiTheme.colors.success}> live</Text>
      </Box>
    </Box>
  );
};
