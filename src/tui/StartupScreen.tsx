import React, { useEffect, useState } from 'react';
import { Box, Text } from 'ink';
import { tuiTheme } from './theme';

interface StartupScreenProps {
  cwd?: string;
  projectName?: string;
  providerName: string;
  modelName: string;
  input: string;
  terminalWidth: number;
  terminalHeight: number;
}

const STARTUP_COMMANDS = ['/plan', '/debug', '/tools', '/model', '/provider'];

const WIDE_ZERO_LOGO = [
  ':::::::::  :::::::::: :::::::::   :::::::: ',
  '     :+:   :+:        :+:    :+: :+:    :+:',
  '    +:+    +:+        +:+    +:+ +:+    +:+',
  '   +#+     +#++:++#   +#++:++#:  +#+    +:+',
  '  +#+      +#+        +#+    +#+ +#+    +#+',
  ' #+#       #+#        #+#    #+# #+#    #+#',
  '#########  ########## ###    ###  ######## ',
];

const COMPACT_ZERO_LOGO = [
  ' _______  _______  ______    _______ ',
  '|       ||       ||    _ |  |       |',
  '|____   ||    ___||   | ||  |   _   |',
  ' ____|  ||   |___ |   |_||_ |  | |  |',
  '| ______||    ___||    __  ||  |_|  |',
  '| |_____ |   |___ |   |  | ||       |',
  '|_______||_______||___|  |_||_______|',
];

export const StartupScreen: React.FC<StartupScreenProps> = ({
  cwd = process.cwd(),
  projectName = 'zero',
  providerName,
  modelName,
  input,
  terminalWidth,
  terminalHeight,
}) => {
  const width = Math.max(60, terminalWidth - 1);
  const height = Math.max(20, terminalHeight);
  const promptWidth = clamp(width - 14, 48, 150);

  return (
    <Box
      flexDirection="column"
      width={width}
      minHeight={height}
      backgroundColor={tuiTheme.colors.background}
    >
      <Header
        cwd={cwd}
        projectName={projectName}
        providerName={providerName}
        modelName={modelName}
        terminalWidth={width}
      />

      {/* This grow region keeps the splash optically centered between header and prompt. */}
      <Box
        flexGrow={1}
        flexDirection="column"
        justifyContent="center"
        alignItems="center"
        paddingX={1}
      >
        <ZeroLogo terminalWidth={width} />
        <Box marginTop={1}>
          <Text color={tuiTheme.colors.brand} bold>
            terminal coding agent
          </Text>
        </Box>
        <CommandChips commands={STARTUP_COMMANDS} />
      </Box>

      <Box flexShrink={0} flexDirection="column" alignItems="center" paddingBottom={1}>
        <PromptBox input={input} width={promptWidth} />
        <ShortcutHints width={promptWidth} />
      </Box>
    </Box>
  );
};

interface HeaderProps {
  cwd: string;
  projectName: string;
  providerName: string;
  modelName: string;
  terminalWidth: number;
}

export const Header: React.FC<HeaderProps> = ({
  cwd,
  projectName,
  providerName,
  modelName,
  terminalWidth,
}) => {
  const compact = terminalWidth < 100;
  const headerWidth = Math.max(48, terminalWidth - 4);
  const displayCwd = compact ? truncateMiddle(cwd, Math.max(18, terminalWidth - 46)) : cwd;

  return (
    <Box
      width={headerWidth}
      borderStyle="round"
      borderColor={tuiTheme.colors.border}
      paddingX={1}
      marginX={1}
      marginTop={1}
      flexDirection={compact ? 'column' : 'row'}
      justifyContent="space-between"
    >
      <Box flexDirection="row">
        <Text color={tuiTheme.colors.brandStrong} bold>ZERO</Text>
        <Text color={tuiTheme.colors.subtle}>  |  </Text>
        <Text color={tuiTheme.colors.muted}>cwd: </Text>
        <Text color={tuiTheme.colors.text}>{displayCwd}</Text>
        <Text color={tuiTheme.colors.subtle}>  |  </Text>
        <Text color={tuiTheme.colors.muted}>project: </Text>
        <Text color={tuiTheme.colors.text}>{projectName}</Text>
      </Box>

      <Box flexDirection="row" flexShrink={0}>
        <Text color={tuiTheme.colors.muted}>status: </Text>
        <Text color={tuiTheme.colors.success} bold>READY</Text>
        <Text color={tuiTheme.colors.subtle}>  |  </Text>
        <Text color={tuiTheme.colors.muted}>provider: </Text>
        <Text color={tuiTheme.colors.brandStrong} bold>{providerName}</Text>
        <Text color={tuiTheme.colors.text}> / {modelName}</Text>
      </Box>
    </Box>
  );
};

export const ZeroLogo: React.FC<{ terminalWidth: number }> = ({ terminalWidth }) => {
  const logo = terminalWidth >= 92 ? WIDE_ZERO_LOGO : COMPACT_ZERO_LOGO;

  return (
    <Box flexDirection="column" alignItems="center">
      {logo.map((line) => (
        <Text key={line} color={tuiTheme.colors.brandStrong} bold>
          {line}
        </Text>
      ))}
    </Box>
  );
};

export const CommandChips: React.FC<{ commands: string[] }> = ({ commands }) => {
  return (
    <Box
      marginTop={2}
      flexDirection="row"
      justifyContent="center"
      flexWrap="wrap"
    >
      {commands.map((command) => (
        <Box
          key={command}
          borderStyle="round"
          borderColor={tuiTheme.colors.brandStrong}
          paddingX={1}
          marginX={1}
          marginBottom={1}
        >
          <Text color={tuiTheme.colors.brandStrong}>{command}</Text>
        </Box>
      ))}
    </Box>
  );
};

export const PromptBox: React.FC<{ input: string; width: number }> = ({
  input,
  width,
}) => {
  const [cursorVisible, setCursorVisible] = useState(true);
  const placeholder = 'Ask Zero to inspect, edit, explain, or run a command...';

  useEffect(() => {
    const interval = setInterval(() => {
      setCursorVisible((visible) => !visible);
    }, 520);

    return () => clearInterval(interval);
  }, []);

  return (
    <Box
      width={width}
      borderStyle="round"
      borderColor={tuiTheme.colors.brandStrong}
      paddingX={1}
      paddingY={1}
    >
      <Text color={tuiTheme.colors.brandStrong} bold>{'zero > '}</Text>
      {input ? (
        <Text color={tuiTheme.colors.text}>{input}</Text>
      ) : (
        <Text color={tuiTheme.colors.muted}>{placeholder}</Text>
      )}
      <Text
        backgroundColor={cursorVisible ? tuiTheme.colors.brandStrong : tuiTheme.colors.background}
        color={cursorVisible ? tuiTheme.colors.brandStrong : tuiTheme.colors.background}
      >
        {' '}
      </Text>
    </Box>
  );
};

export const ShortcutHints: React.FC<{ width: number }> = ({ width }) => {
  return (
    <Box width={width} marginTop={1} flexDirection="row" flexWrap="wrap">
      <ShortcutKey keyName="Enter" label="sends" />
      <ShortcutKey keyName="Tab" label="accepts command" />
      <ShortcutKey keyName="Ctrl+C" label="exits" />
    </Box>
  );
};

function ShortcutKey({
  keyName,
  label,
}: {
  keyName: string;
  label: string;
}) {
  return (
    <Box marginRight={3} marginBottom={1} flexDirection="row">
      <Box borderStyle="single" borderColor={tuiTheme.colors.border} paddingX={1}>
        <Text color={tuiTheme.colors.text}>{keyName}</Text>
      </Box>
      <Text color={tuiTheme.colors.muted}> {label}</Text>
    </Box>
  );
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

function truncateMiddle(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value;

  const keep = Math.max(4, Math.floor((maxLength - 3) / 2));
  return `${value.slice(0, keep)}...${value.slice(-keep)}`;
}
