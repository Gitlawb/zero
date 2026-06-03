import React, { useState } from 'react';
import { Box, Text, useInput } from 'ink';
import { getTheme, getAllThemes, type ThemeDefinition, theme } from './theme';

interface ThemePickerProps {
  onSelect: (name: string) => void;
  onCancel: () => void;
}

export const ThemePicker: React.FC<ThemePickerProps> = ({ onSelect, onCancel }) => {
  const themes = getAllThemes();
  const activeTheme = getTheme();
  const [selectedIndex, setSelectedIndex] = useState(() => {
    const index = themes.findIndex(t => t.name === activeTheme.name);
    return Math.max(index, 0);
  });

  const selectedTheme = themes[selectedIndex];

  useInput((input, key) => {
    if (key.escape || (key.ctrl && input === 'c')) {
      onCancel();
      return;
    }

    if (key.upArrow) {
      setSelectedIndex((prev) => Math.max(0, prev - 1));
      return;
    }

    if (key.downArrow) {
      setSelectedIndex((prev) => Math.min(themes.length - 1, prev + 1));
      return;
    }

    if (key.return && selectedTheme) {
      onSelect(selectedTheme.name);
      return;
    }

    const num = parseInt(input, 10);
    if (!Number.isNaN(num) && num >= 1 && num <= themes.length) {
      onSelect(themes[num - 1]!.name);
    }
  });

  return (
    <Box flexDirection="column" padding={1}>
      <Box marginBottom={1}>
        <Text bold color={theme.text.primary}>Theme</Text>
        <Text color={theme.ui.comment}>  Up/Down select · Enter apply · Esc cancel</Text>
      </Box>

      <Box flexDirection="row">
        <Box flexDirection="column" width="45%" paddingRight={2}>
          <Text bold color={theme.text.primary}>Select Theme</Text>
          <Box marginTop={1} flexDirection="column">
            {themes.map((t, index) => {
              const isSelected = index === selectedIndex;
              const isActive = t.name === activeTheme.name;

              return (
                <Box key={t.name} paddingLeft={1}>
                  <Text color={isSelected ? theme.text.accent : theme.text.primary} wrap="truncate">
                    {isSelected ? '> ' : '  '}
                    {index + 1}. {t.name}
                    <Text color={theme.text.secondary}> {t.type === 'light' ? 'Light' : 'Dark'}</Text>
                    {isActive && <Text color={theme.status.success}> current</Text>}
                  </Text>
                </Box>
              );
            })}
          </Box>
        </Box>

        {selectedTheme && (
          <Box flexDirection="column" width="55%" paddingLeft={2}>
            <Text bold color={theme.text.primary}>Preview</Text>
            <Box
              marginTop={1}
              borderStyle="single"
              borderColor={theme.border.default}
              paddingX={1}
              paddingY={1}
              flexDirection="column"
            >
              <Text color={selectedTheme.colors.text.secondary}># function</Text>
              <Text color={selectedTheme.colors.text.accent}>def <Text color={selectedTheme.colors.ui.symbol}>fibonacci</Text><Text color={selectedTheme.colors.text.primary}>(n):</Text></Text>
              <Text color={selectedTheme.colors.text.primary}>    a, b = <Text color={selectedTheme.colors.status.warning}>0</Text>, <Text color={selectedTheme.colors.status.warning}>1</Text></Text>
              <Text color={selectedTheme.colors.text.accent}>    for <Text color={selectedTheme.colors.text.primary}>_ in range(n):</Text></Text>
              <Text color={selectedTheme.colors.text.primary}>        a, b = b, a + b</Text>
              <Text color={selectedTheme.colors.text.accent}>    return <Text color={selectedTheme.colors.text.primary}>a</Text></Text>

              <Box marginTop={1} flexDirection="column">
                <Text color={selectedTheme.colors.text.secondary}>--- a/util.py</Text>
                <Text color={selectedTheme.colors.text.secondary}>+++ b/util.py</Text>
                <Text color={selectedTheme.colors.ui.comment}>@@ -1,3 +1,3 @@</Text>
                <Text color={selectedTheme.colors.text.primary}> def greet(name):</Text>
                <Text backgroundColor={selectedTheme.colors.background.diff.removed} color={selectedTheme.colors.status.error}>-    print("Hello, " + name)</Text>
                <Text backgroundColor={selectedTheme.colors.background.diff.added} color={selectedTheme.colors.status.success}>+    print(f"Hello, {'{'}name{'}'}!")</Text>
                <Text color={selectedTheme.colors.text.primary}>     return name</Text>
              </Box>

              <Box marginTop={1} backgroundColor={selectedTheme.colors.background.input} paddingX={1}>
                <Text color={selectedTheme.colors.text.accent}>{'> '}</Text>
                <Text color={selectedTheme.colors.text.secondary}>█ Type your message or @path/to/file</Text>
              </Box>

              <Box marginTop={1} flexDirection="row" gap={1}>
                <Text color={selectedTheme.colors.text.primary}>██</Text>
                <Text color={selectedTheme.colors.text.accent}>██</Text>
                <Text color={selectedTheme.colors.ui.symbol}>██</Text>
                <Text color={selectedTheme.colors.status.success}>██</Text>
                <Text color={selectedTheme.colors.status.warning}>██</Text>
                <Text color={selectedTheme.colors.status.error}>██</Text>
              </Box>
            </Box>
          </Box>
        )}
      </Box>
    </Box>
  );
};
