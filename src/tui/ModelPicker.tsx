import React, { useMemo, useState } from 'react';
import { Box, Text, useInput } from 'ink';
import type { ZeroModelDefinition } from '../zero-model-registry';
import { getSelectableZeroModels } from './model-selection';

interface ModelPickerProps {
  activeModelId?: string;
  onSelect: (modelId: string) => void;
  onCancel: () => void;
}

export const ModelPicker: React.FC<ModelPickerProps> = ({
  activeModelId,
  onSelect,
  onCancel,
}) => {
  const models = useMemo(() => getSelectableZeroModels(), []);
  const [selectedIndex, setSelectedIndex] = useState(() => {
    const index = models.findIndex((model) => model.id === activeModelId);
    return Math.max(index, 0);
  });

  const selectedModel = models[selectedIndex];

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
      setSelectedIndex((prev) => Math.min(models.length - 1, prev + 1));
      return;
    }

    if (key.return && selectedModel) {
      onSelect(selectedModel.id);
      return;
    }

    const num = parseInt(input, 10);
    if (!Number.isNaN(num) && num >= 1 && num <= models.length) {
      onSelect(models[num - 1]!.id);
    }
  });

  return (
    <Box flexDirection="column" padding={1}>
      <Text bold color="cyan">
        Select Model
      </Text>
      <Text color="gray" dimColor>
        Up/Down to navigate - Enter to select - Esc to cancel
      </Text>

      <Box marginY={1} flexDirection="column">
        {models.map((model, index) => (
          <ModelRow
            key={model.id}
            model={model}
            index={index}
            isSelected={index === selectedIndex}
            isActive={model.id === activeModelId}
          />
        ))}
      </Box>

      {selectedModel && (
        <Box flexDirection="column" marginLeft={2} borderStyle="round" paddingX={1}>
          <Text>
            <Text bold>ID:</Text> {selectedModel.id}
          </Text>
          <Text>
            <Text bold>Provider:</Text> {selectedModel.provider}
          </Text>
          <Text>
            <Text bold>Context:</Text> {formatTokens(selectedModel.context.contextWindow)} input / {formatTokens(selectedModel.context.maxOutputTokens)} output
          </Text>
          <Text>
            <Text bold>Capabilities:</Text> {selectedModel.capabilities.join(', ')}
          </Text>
          {selectedModel.reasoningEfforts && (
            <Text>
              <Text bold>Effort:</Text> {selectedModel.reasoningEfforts.join(', ')}
            </Text>
          )}
          {selectedModel.description && (
            <Text color="gray" dimColor>
              {selectedModel.description}
            </Text>
          )}
        </Box>
      )}

      <Box marginTop={1}>
        <Text color="gray" dimColor>
          Press 1-{models.length} for quick selection. This changes the current TUI session only.
        </Text>
      </Box>
    </Box>
  );
};

function ModelRow({
  model,
  index,
  isSelected,
  isActive,
}: {
  model: ZeroModelDefinition;
  index: number;
  isSelected: boolean;
  isActive: boolean;
}) {
  return (
    <Box paddingLeft={1}>
      <Text color={isSelected ? 'green' : 'white'}>
        {isSelected ? '> ' : '  '}
        {index + 1}. {model.displayName}
        <Text color="gray"> ({model.provider})</Text>
        {isActive && <Text color="blue"> current</Text>}
      </Text>
    </Box>
  );
}

function formatTokens(value: number): string {
  if (value >= 1_000_000) return `${Math.round(value / 1_000_000)}M`;
  if (value >= 1_000) return `${Math.round(value / 1_000)}K`;
  return String(value);
}
