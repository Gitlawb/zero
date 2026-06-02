import React, { useState } from 'react';
import { Box, Text, useInput } from 'ink';
import { configManager } from '../config/manager';
import type { ProviderProfile } from '../config/types';

interface ProviderPickerProps {
  onSelect: (name: string) => void;
  onCancel: () => void;
  onAddNew: () => void;
}

export const ProviderPicker: React.FC<ProviderPickerProps> = ({ onSelect, onCancel, onAddNew }) => {
  const providers = configManager.listProviders();
  const activeProvider = configManager.getActiveProvider()?.name;

  const totalItems = providers.length + 1; // +1 for "Add new"
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
        // Last item = Add new
        onAddNew();
      }
      return;
    }

    // Quick select by number
    const num = parseInt(input, 10);
    if (!isNaN(num) && num >= 1 && num <= totalItems) {
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
      <Text bold color="cyan">
        Select Provider
      </Text>
      <Text color="gray" dimColor>
        ↑↓ to navigate • Enter to select • Esc to cancel
      </Text>

      <Box marginY={1} flexDirection="column">
        {providers.map((provider, index) => {
          const isSelected = index === selectedIndex;
          const isActive = provider.name === activeProvider;

          return (
            <Box key={provider.name} paddingLeft={1}>
              <Text color={isSelected ? 'green' : 'white'}>
                {isSelected ? '› ' : '  '}
                {provider.name}
                {isActive && <Text color="blue"> (current)</Text>}
              </Text>
            </Box>
          );
        })}

        {/* Add new option */}
        <Box paddingLeft={1}>
          <Text color={selectedIndex === providers.length ? 'green' : 'cyan'}>
            {selectedIndex === providers.length ? '› ' : '  '}
            + Add new provider...
          </Text>
        </Box>
      </Box>

      {providers[selectedIndex] && (
        <Box flexDirection="column" marginLeft={2} borderStyle="round" paddingX={1}>
          <Text>
            <Text bold>Model:</Text> {providers[selectedIndex].model}
          </Text>
          <Text>
            <Text bold>Base URL:</Text> {providers[selectedIndex].baseURL}
          </Text>
          {providers[selectedIndex].description && (
            <Text>
              <Text bold>Description:</Text> {providers[selectedIndex].description}
            </Text>
          )}
        </Box>
      )}

      <Box marginTop={1}>
        <Text color="gray" dimColor>
          Press 1-{totalItems} for quick selection
        </Text>
      </Box>
    </Box>
  );
};
