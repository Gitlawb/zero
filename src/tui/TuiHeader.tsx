import React from 'react';
import { Box, Text } from 'ink';
import { basename } from 'path';
import { tuiTheme } from './theme';
import type { TuiModeState } from './types';

interface TuiHeaderProps extends TuiModeState {
  providerName: string;
  modelName: string;
  cwd?: string;
}

export const TuiHeader: React.FC<TuiHeaderProps> = ({
  providerName,
  modelName,
  cwd = process.cwd(),
  isPlanMode,
  debugMode,
  toolsEnabled,
  isThinking,
}) => {
  const workspace = basename(cwd) || cwd;

  return (
    <Box
      borderStyle="single"
      borderColor={isThinking ? tuiTheme.colors.warning : tuiTheme.colors.border}
      paddingX={1}
      flexDirection="row"
      justifyContent="space-between"
    >
      <Box flexDirection="row">
        <Text color={tuiTheme.colors.brand} bold>zero</Text>
        <Text color={tuiTheme.colors.muted}> / {workspace}</Text>
      </Box>

      <Box flexDirection="row">
        {isPlanMode && <Text color={tuiTheme.colors.success}>PLAN </Text>}
        {debugMode && <Text color={tuiTheme.colors.warning}>DEBUG </Text>}
        {!toolsEnabled && <Text color={tuiTheme.colors.danger}>TOOLS OFF </Text>}
        <Text color={tuiTheme.colors.brand}>{providerName}</Text>
        <Text color={tuiTheme.colors.muted}> / </Text>
        <Text color={tuiTheme.colors.model}>{modelName}</Text>
      </Box>
    </Box>
  );
};
