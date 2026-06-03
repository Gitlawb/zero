import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

/**
 * Block "ZERO" wordmark (figlet "Electronic" — outlined glyphs with a dotted
 * fill, the closest faithful terminal match to the splash mockup). Generated
 * from figlet, never an image model (which would fake the glyphs). Lines are
 * padded to equal width at render so the block stays aligned when centered.
 */
const LOGO = [
  " ▄▄▄▄▄▄▄▄▄▄▄  ▄▄▄▄▄▄▄▄▄▄▄  ▄▄▄▄▄▄▄▄▄▄▄  ▄▄▄▄▄▄▄▄▄▄▄",
  "▐░░░░░░░░░░░▌▐░░░░░░░░░░░▌▐░░░░░░░░░░░▌▐░░░░░░░░░░░▌",
  " ▀▀▀▀▀▀▀▀▀█░▌▐░█▀▀▀▀▀▀▀▀▀ ▐░█▀▀▀▀▀▀▀█░▌▐░█▀▀▀▀▀▀▀█░▌",
  "          ▐░▌▐░▌          ▐░▌       ▐░▌▐░▌       ▐░▌",
  " ▄▄▄▄▄▄▄▄▄█░▌▐░█▄▄▄▄▄▄▄▄▄ ▐░█▄▄▄▄▄▄▄█░▌▐░▌       ▐░▌",
  "▐░░░░░░░░░░░▌▐░░░░░░░░░░░▌▐░░░░░░░░░░░▌▐░▌       ▐░▌",
  "▐░█▀▀▀▀▀▀▀▀▀ ▐░█▀▀▀▀▀▀▀▀▀ ▐░█▀▀▀▀█░█▀▀ ▐░▌       ▐░▌",
  "▐░▌          ▐░▌          ▐░▌     ▐░▌  ▐░▌       ▐░▌",
  "▐░█▄▄▄▄▄▄▄▄▄ ▐░█▄▄▄▄▄▄▄▄▄ ▐░▌      ▐░▌ ▐░█▄▄▄▄▄▄▄█░▌",
  "▐░░░░░░░░░░░▌▐░░░░░░░░░░░▌▐░▌       ▐░▌▐░░░░░░░░░░░▌",
  " ▀▀▀▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀▀▀▀  ▀         ▀  ▀▀▀▀▀▀▀▀▀▀▀",
];

export const LOGO_WIDTH = Math.max(...LOGO.map((line) => line.length));

export interface ZeroLogoProps {
  /** Available width (terminal columns minus padding). */
  maxWidth: number;
}

export const ZeroLogo: React.FC<ZeroLogoProps> = ({ maxWidth }) => {
  // Degrade gracefully: compact title rather than a wrapped/broken logo when
  // the terminal is narrower than the wordmark.
  const fits = maxWidth >= LOGO_WIDTH;

  return (
    <Box flexDirection="column" alignItems="center">
      {fits ? (
        LOGO.map((line, i) => (
          <Text key={i} color={theme.accent} bold>
            {line.padEnd(LOGO_WIDTH)}
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
