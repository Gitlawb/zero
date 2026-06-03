import React from 'react';
import { Box } from 'ink';
import { Header } from './startup/Header';
import { ZeroLogo } from './startup/ZeroLogo';
import { CommandChips } from './startup/CommandChips';
import { PromptBox } from './startup/PromptBox';
import { ShortcutHints } from './startup/ShortcutHints';
import { ModeStatus, type AgentMode } from './startup/ModeStatus';

interface StartupScreenProps {
  cwd?: string;
  projectName?: string;
  providerName: string;
  modelName: string;
  input: string;
  terminalWidth: number;
  terminalHeight: number;
  /** Active agent mode shown in the bottom status line. */
  mode?: AgentMode;
}

const PLACEHOLDER = 'Ask Zero to inspect, edit, explain, or run a command...';

/**
 * Minimal startup splash, composed from reusable components in `./startup`.
 *
 * Presentational / controlled: TuiShell owns input and passes the current
 * value + terminal size, so this just lays out the screen. Vertical balance is
 * done with flex (not hardcoded offsets):
 *
 *   ┌ Header ───────────────┐  ← pinned top
 *   │   logo + chips         │  ← flexGrow 1, centered both axes
 *   ├ PromptBox ────────────┤  ← pinned bottom
 *   └ ShortcutHints ────────┘
 *
 * No context / history / session panels render here — those mount only when
 * the user runs /context, /history or /session.
 */
export const StartupScreen: React.FC<StartupScreenProps> = ({
  cwd = process.cwd(),
  projectName = 'zero',
  providerName,
  modelName,
  input,
  terminalWidth,
  terminalHeight,
  mode = 'build',
}) => {
  // Clamp to sane minimums so the layout never collapses on tiny terminals.
  const width = Math.max(60, terminalWidth - 1);
  const height = Math.max(20, terminalHeight);

  return (
    <Box flexDirection="column" width={width} minHeight={height}>
      <Header
        cwd={cwd}
        project={projectName}
        status="READY"
        provider={providerName}
        model={modelName}
        width={width}
      />

      {/* Grow region centers the wordmark + chips between header and prompt. */}
      <Box flexGrow={1} flexDirection="column" justifyContent="center" alignItems="center" paddingX={1}>
        <ZeroLogo maxWidth={width - 4} />
        <Box marginTop={2}>
          <CommandChips />
        </Box>
      </Box>

      {/* Bottom cluster, pinned near the bottom edge. */}
      <Box flexDirection="column" flexShrink={0} paddingBottom={1}>
        <PromptBox value={input} placeholder={PLACEHOLDER} />
        <ShortcutHints />
        <ModeStatus mode={mode} />
      </Box>
    </Box>
  );
};
