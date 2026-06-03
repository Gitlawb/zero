import React, { useState } from 'react';
import { Box, Text, useInput } from 'ink';
import { configManager } from '../config/manager';
import { theme } from './theme';

interface ProviderPickerProps {
  onSelect: (name: string) => void;
  onCancel: () => void;
  onAddNew: () => void;
}

export const ProviderPicker: React.FC<ProviderPickerProps> = ({ onSelect, onCancel, onAddNew }) => {
  const providers = configManager.listProviders();
  const activeProvider = configManager.getActiveProvider()?.name;
  const totalItems = providers.length + 1;
  const [selectedIndex, setSelectedIndex] = useState(0);

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
      setSelectedIndex((prev) => Math.min(totalItems - 1, prev + 1));
      return;
    }

    if (key.return) {
      if (selectedIndex < providers.length) {
        const selected = providers[selectedIndex];
        if (selected) onSelect(selected.name);
      } else {
        onAddNew();
      }
      return;
    }

    const num = parseInt(input, 10);
    if (!Number.isNaN(num) && num >= 1 && num <= totalItems) {
      if (num <= providers.length) {
        const selected = providers[num - 1];
        if (selected) onSelect(selected.name);
      } else {
        onAddNew();
      }
    }
  });

  return (
    <Box flexDirection="column" padding={1}>
      <Text bold color={theme.ui.active}>
        Select Provider
      </Text>
      <Text color={theme.ui.comment}>
        ↑↓ to navigate • Enter to select • Esc to cancel
      </Text>

      <Box marginY={1} flexDirection="column">
        {providers.map((provider, index) => {
          const isSelected = index === selectedIndex;
          const isActive = provider.name === activeProvider;

          return (
            <Box key={provider.name} paddingLeft={1}>
              <Text color={isSelected ? theme.ui.active : theme.text.primary}>
                {isSelected ? '› ' : '  '}
                {provider.name}
                {isActive && <Text color={theme.text.accent}> (current)</Text>}
              </Text>
            </Box>
          );
        })}

        <Box paddingLeft={1}>
          <Text color={selectedIndex === providers.length ? theme.ui.active : theme.text.primary}>
            {selectedIndex === providers.length ? '› ' : '  '}
            + Add new provider...
          </Text>
        </Box>
      </Box>

      {providers[selectedIndex] && (
        <Box flexDirection="column" marginLeft={2} borderStyle="round" paddingX={1} borderColor={theme.border.default}>
          <Text color={theme.text.primary}>
            <Text bold color={theme.text.secondary}>Model:</Text> {providers[selectedIndex].model}
          </Text>
          <Text color={theme.text.primary}>
            <Text bold color={theme.text.secondary}>Base URL:</Text> {providers[selectedIndex].baseURL}
          </Text>
          {providers[selectedIndex].description && (
            <Text color={theme.text.secondary}>
              <Text bold>{' '}</Text>{providers[selectedIndex].description}
            </Text>
          )}
        </Box>
      )}

      <Box marginTop={1}>
        <Text color={theme.ui.comment}>
          Press 1-{totalItems} for quick selection
        </Text>
      </Box>
    </Box>
  );
};
