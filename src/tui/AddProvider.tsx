import React, { useState } from 'react';
import { Box, Text, useInput } from 'ink';
import TextInput from 'ink-text-input';
import { configManager } from '../config/manager';
import { theme } from './theme';

type AddMode = 'choose' | 'opengateway' | 'generic';

interface AddProviderProps {
  onDone: (providerName?: string) => void;
  onCancel: () => void;
}

export const AddProvider: React.FC<AddProviderProps> = ({ onDone, onCancel }) => {
  const completionTimeoutRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const [mode, setMode] = useState<AddMode>('choose');
  const [selectedOption, setSelectedOption] = useState(0);
  const [openGatewayStep, setOpenGatewayStep] = useState(0);
  const [openGatewayKey, setOpenGatewayKey] = useState('');
  const [openGatewayModel, setOpenGatewayModel] = useState('mimo-v2.5-pro');
  const [name, setName] = useState('');
  const [baseURL, setBaseURL] = useState('https://api.openai.com/v1');
  const [apiKey, setApiKey] = useState('');
  const [model, setModel] = useState('gpt-4o');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);

  const clearCompletionTimeout = () => {
    if (completionTimeoutRef.current) {
      clearTimeout(completionTimeoutRef.current);
      completionTimeoutRef.current = null;
    }
  };

  React.useEffect(() => clearCompletionTimeout, []);

  const completeAfterSuccess = (providerName: string) => {
    clearCompletionTimeout();
    completionTimeoutRef.current = setTimeout(() => {
      completionTimeoutRef.current = null;
      onDone(providerName);
    }, 1200);
  };

  useInput((input, key) => {
    if (key.escape) {
      clearCompletionTimeout();
      if (mode === 'choose') {
        onCancel();
      } else {
        setMode('choose');
        setOpenGatewayStep(0);
        setError('');
        setSuccess(false);
        setSelectedOption(0);
      }
      return;
    }

    if (mode === 'choose') {
      if (key.upArrow) {
        setSelectedOption((prev) => Math.max(0, prev - 1));
        return;
      }
      if (key.downArrow) {
        setSelectedOption((prev) => Math.min(1, prev + 1));
        return;
      }
      if (key.return) {
        if (selectedOption === 0) {
          setMode('opengateway');
          setOpenGatewayStep(0);
        } else {
          setMode('generic');
        }
        return;
      }

      if (input === '1') {
        setMode('opengateway');
        setOpenGatewayStep(0);
      }
      if (input === '2') {
        setMode('generic');
      }
    }
  });

  const saveOpenGateway = () => {
    const apiKey = openGatewayKey.trim();
    const apiModel = openGatewayModel.trim();

    if (!apiKey) {
      setError('API key is required');
      return;
    }

    if (!apiModel) {
      setError('Model is required');
      return;
    }

    const profileName = 'opengateway';
    configManager.addProvider({
      name: profileName,
      baseURL: 'https://opengateway.gitlawb.com/v1',
      apiKey,
      model: apiModel,
      description: 'OpenGateway',
    });

    setSuccess(true);
    completeAfterSuccess(profileName);
  };

  const saveGeneric = () => {
    if (!name.trim() || !baseURL.trim() || !model.trim()) {
      setError('Name, Base URL, and Model are required');
      return;
    }

    configManager.addProvider({
      name: name.trim(),
      baseURL: baseURL.trim(),
      apiKey: apiKey.trim() || undefined,
      model: model.trim(),
      description: 'Custom OpenAI-compatible',
    });

    setSuccess(true);
    completeAfterSuccess(name.trim());
  };

  if (mode === 'choose') {
    return (
      <Box flexDirection="column" padding={1}>
        <Text bold color={theme.ui.active}>Add New Provider</Text>
        <Text color={theme.ui.comment}>Esc to go back • ↑↓ to navigate • Enter to select</Text>

        <Box marginY={1} flexDirection="column">
          <Text color={selectedOption === 0 ? theme.ui.active : theme.text.primary}>
            {selectedOption === 0 ? '› ' : '  '}1. Add OpenGateway (recommended)
          </Text>
          {selectedOption === 0 && (
            <Text color={theme.ui.comment}>
              {'   '}You'll be asked for your ogw_live_... API key
            </Text>
          )}

          <Text color={selectedOption === 1 ? theme.ui.active : theme.text.primary}>
            {selectedOption === 1 ? '› ' : '  '}2. Add custom OpenAI-compatible provider
          </Text>
          {selectedOption === 1 && (
            <Text color={theme.ui.comment}>
              {'   '}For Groq, OpenAI, Ollama, etc.
            </Text>
          )}
        </Box>
      </Box>
    );
  }

  if (mode === 'opengateway') {
    if (success) {
      return (
        <Box flexDirection="column" padding={1}>
          <Text color={theme.status.success} bold>
            ✓ OpenGateway provider added successfully!
          </Text>
          <Text color={theme.ui.comment}>
            It is now your active provider.
          </Text>
        </Box>
      );
    }

    return (
      <Box flexDirection="column" padding={1}>
        <Text bold color={theme.ui.active}>Add OpenGateway Provider</Text>
        <Text color={theme.ui.comment}>Esc to go back</Text>

        {openGatewayStep === 0 && (
          <Box marginTop={1} flexDirection="column">
            <Text color={theme.status.warning}>Step 1/2 — Enter your OpenGateway API key</Text>
            <Text color={theme.ui.comment}>
              You can get one at https://opengateway.gitlawb.com
            </Text>
            <Box marginTop={1}>
              <Text color={theme.text.primary}>API Key: </Text>
              <TextInput
                value={openGatewayKey}
                onChange={setOpenGatewayKey}
                mask="*"
                placeholder="ogw_live_..."
              />
            </Box>
            <Box marginTop={1}>
              <Text color={theme.ui.comment}>Press Enter to continue</Text>
            </Box>
            {error && <Text color={theme.status.error}>⚠ {error}</Text>}
            <TextInput
              value=""
              onChange={() => {}}
              onSubmit={() => {
                if (openGatewayKey.trim()) {
                  setOpenGatewayStep(1);
                  setError('');
                } else {
                  setError('API key cannot be empty');
                }
              }}
            />
          </Box>
        )}

        {openGatewayStep === 1 && (
          <Box marginTop={1} flexDirection="column">
            <Text color={theme.status.warning}>Step 2/2 — Model name</Text>
            <Box marginTop={1}>
              <Text color={theme.text.primary}>Model: </Text>
              <TextInput value={openGatewayModel} onChange={setOpenGatewayModel} />
            </Box>
            {error && <Text color={theme.status.error}>⚠ {error}</Text>}
            <Box marginTop={1}>
              <Text color={theme.ui.comment}>Press Enter to save</Text>
            </Box>
            <TextInput
              value=""
              onChange={() => {}}
              onSubmit={saveOpenGateway}
            />
          </Box>
        )}
      </Box>
    );
  }

  if (mode === 'generic') {
    if (success) {
      return (
        <Box flexDirection="column" padding={1}>
          <Text color={theme.status.success} bold>
            ✓ Provider added successfully!
          </Text>
        </Box>
      );
    }

    return (
      <Box flexDirection="column" padding={1}>
        <Text bold color={theme.ui.active}>Add Custom Provider</Text>
        <Text color={theme.ui.comment}>Esc to go back</Text>

        <Box marginTop={1}>
          <Text color={theme.text.primary}>Name: </Text>
          <TextInput value={name} onChange={setName} />
        </Box>
        <Box>
          <Text color={theme.text.primary}>Base URL: </Text>
          <TextInput value={baseURL} onChange={setBaseURL} />
        </Box>
        <Box>
          <Text color={theme.text.primary}>API Key: </Text>
          <TextInput value={apiKey} onChange={setApiKey} mask="*" />
        </Box>
        <Box>
          <Text color={theme.text.primary}>Model: </Text>
          <TextInput value={model} onChange={setModel} />
        </Box>

        {error && <Text color={theme.status.error}>{error}</Text>}

        <Box marginTop={1}>
          <Text color={theme.ui.comment}>Press Enter to save</Text>
        </Box>

        <TextInput
          value=""
          onChange={() => {}}
          onSubmit={saveGeneric}
        />
      </Box>
    );
  }

  return null;
};
