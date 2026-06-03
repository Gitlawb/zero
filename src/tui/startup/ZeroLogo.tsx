import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

/**
 * Block "ZERO" wordmark (figlet "ANSI Shadow").
 *
 * Terminals render text, not images — real monospace block glyphs are the
 * faithful way to draw the splash logo (and the established Zero wordmark).
 * Never generate this from an image model; it fakes the box-drawing glyphs.
 */
const LOGO = [
  '███████╗ ███████╗ ██████╗   ██████╗ ',
  '╚══███╔╝ ██╔════╝ ██╔══██╗ ██╔═══██╗',
  '  ███╔╝  █████╗   ██████╔╝ ██║   ██║',
  ' ███╔╝   ██╔══╝   ██╔══██╗ ██║   ██║',
  '███████╗ ███████╗ ██║  ██║ ╚██████╔╝',
  '╚══════╝ ╚══════╝ ╚═╝  ╚═╝  ╚═════╝ ',
];

export const LOGO_WIDTH = Math.max(...LOGO.map((line) => line.length));

export interface ZeroLogoProps {
  /** Available width (terminal columns minus padding). */
  maxWidth: number;
}

export const ZeroLogo: React.FC<ZeroLogoProps> = ({ maxWidth }) => {
  // Degrade gracefully: render a compact title rather than a wrapped/broken
  // logo when the terminal is narrower than the wordmark.
  const fits = maxWidth >= LOGO_WIDTH;

  return (
    <Box flexDirection="column" alignItems="center">
      {fits ? (
        LOGO.map((line, i) => (
          <Text key={i} color={theme.accent} bold>
            {line}
          </Text>
        ))
      ) : (
        <Text color={theme.accent} bold>
          ▌ ZERO ▐
        </Text>
      )}

      <Box marginTop={1}>
        <Text color={theme.muted}>terminal coding agent</Text>
      </Box>
    </Box>
  );
};
