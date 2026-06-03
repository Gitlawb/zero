import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

const LOGO_LINES = [
  '███████╗███████╗██████╗  ██████╗',
  '╚══███╔╝██╔════╝██╔══██╗██╔═══██╗',
  '  ███╔╝ █████╗  ██████╔╝██║   ██║',
  ' ███╔╝  ██╔══╝  ██╔══██╗██║   ██║',
  '███████╗███████╗██║  ██║╚██████╔╝',
  '╚══════╝╚══════╝╚═╝  ╚═╝ ╚═════╝',
];

const LOGO_WIDTH = Math.max(...LOGO_LINES.map((line) => line.length));

interface LogoProps {
  maxWidth?: number;
}

export const Logo: React.FC<LogoProps> = ({ maxWidth = LOGO_WIDTH }) => {
  const [frame, setFrame] = React.useState(0);
  const animationsDisabled = process.env.NO_ANIMATION === '1' || process.env.NO_ANIMATIONS === '1' || process.env.CI === 'true';

  React.useEffect(() => {
    if (animationsDisabled) return;
    const timer = setInterval(() => setFrame((value) => value + 1), 650);
    timer.unref?.();
    return () => clearInterval(timer);
  }, [animationsDisabled]);

  const cursor = animationsDisabled || frame % 2 === 0 ? '▌' : ' ';
  const canRenderWordmark = maxWidth >= LOGO_WIDTH;

  return (
    <Box flexDirection="column" marginBottom={1}>
      {canRenderWordmark ? (
        LOGO_LINES.map((line, index) => (
          <Text key={line} color={index === LOGO_LINES.length - 1 ? theme.ui.comment : theme.ui.active} bold wrap="truncate">
            {line}
          </Text>
        ))
      ) : (
        <Text color={theme.ui.active} bold>ZERO</Text>
      )}
      <Box flexDirection="row">
        <Text color={theme.ui.comment}>  terminal agent </Text>
        <Text color={theme.text.accent}>{cursor}</Text>
      </Box>
    </Box>
  );
};
