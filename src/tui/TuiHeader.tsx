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
  const statusLabel = isThinking ? 'RUNNING' : 'READY';
  const statusColor = isThinking ? tuiTheme.colors.warning : tuiTheme.colors.success;

  return (
    <Box flexDirection="column">
      <Box
        paddingX={1}
        flexDirection="row"
        justifyContent="space-between"
      >
        <Box flexDirection="row">
          <Text color={tuiTheme.colors.brand} bold>
            ZERO
          </Text>
          <Text color={tuiTheme.colors.muted}>
            {'  '}cwd{' '}
          </Text>
          <Text color={tuiTheme.colors.text}>
            {workspace}
          </Text>
        </Box>

        <Box flexDirection="row">
          <Text color={statusColor} bold>
            {statusLabel}
          </Text>
        </Box>
      </Box>

      <Box paddingX={1} flexDirection="row" justifyContent="space-between">
        <Box flexDirection="row">
          <ModePill label="plan" active={isPlanMode} color={tuiTheme.colors.success} />
          <ModePill label="debug" active={debugMode} color={tuiTheme.colors.warning} />
          <ModePill label="tools" active={toolsEnabled} color={toolsEnabled ? tuiTheme.colors.success : tuiTheme.colors.danger} />
        </Box>

        <Box flexDirection="row">
          <Text color={tuiTheme.colors.muted}>provider </Text>
          <Text color={tuiTheme.colors.brand} bold>{providerName}</Text>
          <Text color={tuiTheme.colors.subtle}> / </Text>
          <Text color={tuiTheme.colors.model}>{modelName}</Text>
        </Box>
      </Box>
    </Box>
  );
};

function ModePill({
  label,
  active,
  color,
}: {
  label: string;
  active: boolean;
  color: string;
}) {
  const text = active ? label.toUpperCase() : label;

  return (
    <Text color={active ? color : tuiTheme.colors.subtle}>
      [{text}]{' '}
    </Text>
  );
}
