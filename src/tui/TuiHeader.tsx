import React from 'react';
import { tuiTheme } from './theme';
import { Box, Text } from 'ink';
import type { TuiModeState } from './types';

interface TuiHeaderProps extends TuiModeState {
  providerName: string;
  modelName: string;
  cwd?: string;
  /** Most recently read/edited file, shown as the working location. */
  activeFile?: string;
  /** Current git branch + ahead/behind counts. */
  branch?: string;
  ahead?: number;
  behind?: number;
}

// Make the active path readable: collapse $HOME to ~, dim the directories,
// bold the filename.
function splitPath(file: string): { dir: string; name: string } {
  let p = file.replace(/\\/g, '/');
  const home = (process.env.HOME || process.env.USERPROFILE || '').replace(/\\/g, '/');
  if (home && p.toLowerCase().startsWith(home.toLowerCase())) p = `~${p.slice(home.length)}`;
  const idx = p.lastIndexOf('/');
  return idx >= 0 ? { dir: p.slice(0, idx + 1), name: p.slice(idx + 1) } : { dir: '', name: p };
}

/**
 * Working-view header: the active file location on the left, git branch +
 * ahead/behind on the right. Model/provider/usage now live in the status bar.
 */
export const TuiHeader: React.FC<TuiHeaderProps> = ({
  cwd = process.cwd(),
  activeFile,
  branch,
  ahead = 0,
  behind = 0,
}) => {
  const { dir, name } = splitPath(activeFile || cwd);

  return (
    <Box paddingX={1} flexDirection="row" justifyContent="space-between">
      {/* Left: active file location */}
      <Box flexDirection="row">
        {dir ? <Text color={tuiTheme.colors.muted}>{dir}</Text> : null}
        <Text color={tuiTheme.colors.text} bold>
          {name}
        </Text>
      </Box>

      {/* Right: git branch + ahead/behind */}
      {branch ? (
        <Box flexDirection="row">
          <Text color={tuiTheme.colors.accent}>{branch}</Text>
          {ahead > 0 ? <Text color={tuiTheme.colors.muted}>{` ↑${ahead}`}</Text> : null}
          {behind > 0 ? <Text color={tuiTheme.colors.muted}>{` ↓${behind}`}</Text> : null}
        </Box>
      ) : null}
    </Box>
  );
};
