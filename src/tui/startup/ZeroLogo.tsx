import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

/**
 * "ZERO" wordmark — figlet "Rebel" (a 3D font: solid front faces + a shaded
 * drop-shadow). Generated from figlet, never an image model.
 *
 * Color: rendered two-tone so the 3D reads correctly — the solid front faces
 * in bright cyan and the shaded faces dim. Each line is split into runs of the
 * same glyph class so a run can carry one color; lines are padded to equal
 * width so the block stays aligned when centered.
 */
const LOGO = [
  " ███████████ ██████████ ███████████      ███████",
  "▒█▒▒▒▒▒▒███ ▒▒███▒▒▒▒▒█▒▒███▒▒▒▒▒███   ███▒▒▒▒▒███",
  "▒     ███▒   ▒███  █ ▒  ▒███    ▒███  ███     ▒▒███",
  "     ███     ▒██████    ▒██████████  ▒███      ▒███",
  "    ███      ▒███▒▒█    ▒███▒▒▒▒▒███ ▒███      ▒███",
  "  ████     █ ▒███ ▒   █ ▒███    ▒███ ▒▒███     ███",
  " ███████████ ██████████ █████   █████ ▒▒▒███████▒",
  "▒▒▒▒▒▒▒▒▒▒▒ ▒▒▒▒▒▒▒▒▒▒ ▒▒▒▒▒   ▒▒▒▒▒    ▒▒▒▒▒▒▒",
];

export const LOGO_WIDTH = Math.max(...LOGO.map((line) => line.length));

type GlyphClass = 'solid' | 'shade' | 'space';

function classify(ch: string): GlyphClass {
  if (ch === ' ') return 'space';
  if (ch === '\u2592' || ch === '\u2591' || ch === '\u2593') return 'shade'; // ▒ ░ ▓
  return 'solid';
}

// Collapse a line into runs of the same glyph class (fewer <Text> spans).
function toRuns(line: string): Array<{ cls: GlyphClass; text: string }> {
  const runs: Array<{ cls: GlyphClass; text: string }> = [];
  for (const ch of line) {
    const cls = classify(ch);
    const last = runs[runs.length - 1];
    if (last && last.cls === cls) last.text += ch;
    else runs.push({ cls, text: ch });
  }
  return runs;
}

export interface ZeroLogoProps {
  /** Available width (terminal columns minus padding). */
  maxWidth: number;
}

export const ZeroLogo: React.FC<ZeroLogoProps> = ({ maxWidth }) => {
  // Degrade gracefully on terminals narrower than the wordmark.
  if (maxWidth < LOGO_WIDTH) {
    return (
      <Box flexDirection="column" alignItems="center">
        <Text color={theme.accentBright} bold>
          ▌ ZERO ▐
        </Text>
        <Box marginTop={1}>
          <Text color={theme.muted}>terminal coding agent</Text>
        </Box>
      </Box>
    );
  }

  return (
    <Box flexDirection="column" alignItems="center">
      {LOGO.map((line, i) => (
        <Text key={i}>
          {toRuns(line.padEnd(LOGO_WIDTH)).map((run, j) => {
            if (run.cls === 'space') return <Text key={j}>{run.text}</Text>;
            if (run.cls === 'solid')
              return (
                <Text key={j} color={theme.accentBright} bold>
                  {run.text}
                </Text>
              );
            // Shaded drop-shadow: dim cyan for depth.
            return (
              <Text key={j} color={theme.accent} dimColor>
                {run.text}
              </Text>
            );
          })}
        </Text>
      ))}
      <Box marginTop={1}>
        <Text color={theme.muted}>terminal coding agent</Text>
      </Box>
    </Box>
  );
};
