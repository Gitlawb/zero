import React from 'react';
import { Box, Text } from 'ink';
import { theme } from './theme';

export const Logo: React.FC = () => {
  const [frame, setFrame] = React.useState(0);
  const animationsDisabled = process.env.NO_ANIMATION === '1' || process.env.NO_ANIMATIONS === '1' || process.env.CI === 'true';

  React.useEffect(() => {
    if (animationsDisabled) return;
    const timer = setInterval(() => setFrame((value) => value + 1), 650);
    timer.unref?.();
    return () => clearInterval(timer);
  }, [animationsDisabled]);

  const lines = [
    '   ____  ___  ____  ____ ',
    '  /_  / / _ \\/ __ \\/ __ \\',
    '   / /_/  __/ /_/ / /_/ /',
    '  /___/\\___/\\____/\\____/ ',
  ];
  const cursor = animationsDisabled || frame % 2 === 0 ? '▌' : ' ';

  return (
    <Box flexDirection="column" marginBottom={1}>
      {lines.map((line, index) => (
        <Text key={line} color={index === 1 ? theme.text.accent : theme.ui.active} bold wrap="truncate">
          {line}
        </Text>
      ))}
      <Box flexDirection="row">
        <Text color={theme.ui.comment}>  terminal agent </Text>
        <Text color={theme.text.accent}>{cursor}</Text>
      </Box>
    </Box>
  );
};
