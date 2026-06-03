import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

export type AgentMode = 'build' | 'plan' | 'auto-accept' | 'bypass';

// Icon / label / color per mode — cycled with shift+tab (Claude-Code style).
const MODES: Record<AgentMode, { icon: string; label: string; color: string }> = {
  build: { icon: '⏵', label: 'build mode', color: theme.accent },
  plan: { icon: '⏸', label: 'plan mode', color: theme.accentBright },
  'auto-accept': { icon: '⏵⏵', label: 'auto-accept edits', color: theme.ok },
  bypass: { icon: '⏵⏵', label: 'bypass permissions', color: theme.warn },
};

export interface ModeStatusProps {
  mode?: AgentMode;
}

const Dot: React.FC = () => <Text color={theme.label}>{'   ·   '}</Text>;

/**
 * Bottom status/mode line, Claude-Code style: the active mode with a chevron
 * indicator, the shift+tab cycle affordance, and a slash-command hint.
 * Presentational — the shell owns the actual shift+tab cycling; this just
 * reflects the current `mode`.
 */
export const ModeStatus: React.FC<ModeStatusProps> = ({ mode = 'build' }) => {
  const m = MODES[mode];
  return (
    <Box paddingX={1} flexShrink={0}>
      <Text color={m.color} bold>
        {m.icon} {m.label}
      </Text>
      <Text color={theme.muted}> (shift+tab to cycle)</Text>
      <Dot />
      <Text color={theme.muted}>/ for commands</Text>
    </Box>
  );
};
