import React, { useState } from 'react';
import { Box, Text, useInput } from 'ink';
import { configManager } from '../config/manager';
import { tuiTheme } from './theme';

interface ProviderPickerProps {
  onSelect: (name: string) => void;
  onCancel: () => void;
  onAddNew: () => void;
}

export const ProviderPicker: React.FC<ProviderPickerProps> = ({
  onSelect,
  onCancel,
  onAddNew,
}) => {
  const providers = configManager.listProviders();
  const activeProvider = configManager.getActiveProvider()?.name;
  const totalItems = providers.length + 1;
  const [selectedIndex, setSelectedIndex] = useState(0);
  const selectedProvider = providers[selectedIndex];
  const addNewSelected = selectedIndex === providers.length;

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
      if (addNewSelected) onAddNew();
      else if (selectedProvider) onSelect(selectedProvider.name);
      return;
    }

    const num = parseInt(input, 10);
    if (!Number.isNaN(num) && num >= 1 && num <= totalItems) {
      if (num <= providers.length) {
        const provider = providers[num - 1];
        if (provider) onSelect(provider.name);
      } else {
        onAddNew();
      }
    }
  });

  return (
    <Box flexDirection="column" padding={1}>
      <Text bold color={tuiTheme.colors.brand}>Provider Profiles</Text>
      <Text color={tuiTheme.colors.muted} dimColor>
        Up/Down moves, Enter selects, Esc returns
      </Text>

      <Box marginY={1} flexDirection="column">
        {providers.length === 0 && (
          <Text color={tuiTheme.colors.muted} dimColor>
            No saved providers yet.
          </Text>
        )}

        {providers.map((provider, index) => {
          const isSelected = index === selectedIndex;
          const isActive = provider.name === activeProvider;

          return (
            <Box key={provider.name} paddingLeft={1}>
              <Text color={isSelected ? tuiTheme.colors.accent : tuiTheme.colors.text}>
                {isSelected ? '> ' : '  '}
                {index + 1}. {provider.name}
                <Text color={tuiTheme.colors.muted}> / {provider.model}</Text>
                {isActive && <Text color={tuiTheme.colors.brand}> current</Text>}
              </Text>
            </Box>
          );
        })}

        <Box paddingLeft={1}>
          <Text color={addNewSelected ? tuiTheme.colors.accent : tuiTheme.colors.brand}>
            {addNewSelected ? '> ' : '  '}
            {providers.length + 1}. Add provider
          </Text>
        </Box>
      </Box>

      {selectedProvider && (
        <Box flexDirection="column" marginLeft={2} borderStyle="single" borderColor={tuiTheme.colors.border} paddingX={1}>
          <Text><Text bold>Model:</Text> {selectedProvider.model}</Text>
          {selectedProvider.provider && <Text><Text bold>Provider:</Text> {selectedProvider.provider}</Text>}
          <Text><Text bold>Base URL:</Text> {selectedProvider.baseURL}</Text>
          {selectedProvider.description && <Text><Text bold>Description:</Text> {selectedProvider.description}</Text>}
        </Box>
      )}

      <Box marginTop={1}>
        <Text color={tuiTheme.colors.muted} dimColor>
          Press 1-{totalItems} for quick selection
        </Text>
      </Box>
    </Box>
  );
};
