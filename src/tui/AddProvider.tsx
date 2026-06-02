import React, { useState } from 'react';
import { Box, Text, useInput } from 'ink';
import TextInput from 'ink-text-input';
import { configManager } from '../config/manager';

type AddMode = 'choose' | 'opengateway' | 'generic';

interface AddProviderProps {
  onDone: (providerName?: string) => void;
  onCancel: () => void;
}

export const AddProvider: React.FC<AddProviderProps> = ({ onDone, onCancel }) => {
  const [mode, setMode] = useState<AddMode>('choose');

  // For the choose menu
  const [selectedOption, setSelectedOption] = useState(0); // 0 = OpenGateway, 1 = Generic

  // For the OpenGateway form (step based)
  const [ogwFormStep, setOgwFormStep] = useState(0); // 0 = API Key, 1 = Model, 2 = Done

  // OpenGateway fields
  const [ogwKey, setOgwKey] = useState('');
  const [ogwModel, setOgwModel] = useState('mimo-v2.5-pro');

  // Generic fields
  const [name, setName] = useState('');
  const [baseURL, setBaseURL] = useState('https://api.openai.com/v1');
  const [apiKey, setApiKey] = useState('');
  const [model, setModel] = useState('gpt-4o');

  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);

  useInput((input, key) => {
    if (key.escape) {
      if (mode === 'choose') {
        onCancel();
      } else {
        // Reset states when going back
        setMode('choose');
        setOgwFormStep(0);
        setError('');
        setSuccess(false);
        setSelectedOption(0);
      }
      return;
    }

    // Arrow key + Enter navigation for the choose menu
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
          setOgwFormStep(0);
        } else {
          setMode('generic');
        }
        return;
      }

      // Still support numbers for convenience
      if (input === '1') {
        setMode('opengateway');
        setOgwFormStep(0);
      }
      if (input === '2') {
        setMode('generic');
      }
    }
  });

  const saveOpenGateway = () => {
    if (!ogwKey.trim()) {
      setError('API key is required');
      return;
    }

    const profileName = 'opengateway';

    configManager.addProvider({
      name: profileName,
      baseURL: 'https://opengateway.gitlawb.com/v1',
      apiKey: ogwKey.trim(),
      model: ogwModel.trim(),
      description: 'OpenGateway',
    });

    setSuccess(true);

    // Auto-close after a short delay so the user sees the success message
    setTimeout(() => {
      onDone(profileName);
    }, 1200);
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
    setTimeout(() => onDone(name.trim()), 1200);
  };

  if (mode === 'choose') {
    return (
      <Box flexDirection="column" padding={1}>
        <Text bold color="cyan">Add New Provider</Text>
        <Text color="gray">Esc to go back • ↑↓ to navigate • Enter to select</Text>

        <Box marginY={1} flexDirection="column">
          <Text color={selectedOption === 0 ? 'greenBright' : 'white'}>
            {selectedOption === 0 ? '› ' : '  '}1. Add OpenGateway (recommended)
          </Text>
          {selectedOption === 0 && (
            <Text color="gray" dimColor>
              {'   '}You'll be asked for your ogw_live_... API key
            </Text>
          )}

          <Text color={selectedOption === 1 ? 'greenBright' : 'white'}>
            {selectedOption === 1 ? '› ' : '  '}2. Add custom OpenAI-compatible provider
          </Text>
          {selectedOption === 1 && (
            <Text color="gray" dimColor>
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
          <Text color="greenBright" bold>
            ✓ OpenGateway provider added successfully!
          </Text>
          <Text color="gray" dimColor>
            It is now your active provider.
          </Text>
        </Box>
      );
    }

    return (
      <Box flexDirection="column" padding={1}>
        <Text bold color="cyan">Add OpenGateway Provider</Text>
        <Text color="gray">Esc to go back</Text>

        {ogwFormStep === 0 && (
          <Box marginTop={1} flexDirection="column">
            <Text color="yellowBright">Step 1/2 — Enter your OpenGateway API key</Text>
            <Text color="gray" dimColor>
              You can get one at https://opengateway.gitlawb.com
            </Text>
            <Box marginTop={1}>
              <Text>API Key: </Text>
              <TextInput
                value={ogwKey}
                onChange={setOgwKey}
                mask="*"
                placeholder="ogw_live_..."
              />
            </Box>
            <Box marginTop={1}>
              <Text color="gray" dimColor>Press Enter to continue</Text>
            </Box>
            <TextInput
              value=""
              onChange={() => {}}
              onSubmit={() => {
                if (ogwKey.trim()) {
                  setOgwFormStep(1);
                  setError('');
                } else {
                  setError('API key cannot be empty');
                }
              }}
            />
          </Box>
        )}

        {ogwFormStep === 1 && (
          <Box marginTop={1} flexDirection="column">
            <Text color="yellowBright">Step 2/2 — Model name</Text>
            <Box marginTop={1}>
              <Text>Model: </Text>
              <TextInput value={ogwModel} onChange={setOgwModel} />
            </Box>
            {error && <Text color="red">⚠ {error}</Text>}
            <Box marginTop={1}>
              <Text color="gray" dimColor>Press Enter to save</Text>
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
          <Text color="greenBright" bold>
            ✓ Provider added successfully!
          </Text>
        </Box>
      );
    }

    return (
      <Box flexDirection="column" padding={1}>
        <Text bold color="cyan">Add Custom Provider</Text>
        <Text color="gray">Esc to go back</Text>

        <Box marginTop={1}>
          <Text>Name: </Text>
          <TextInput value={name} onChange={setName} />
        </Box>
        <Box>
          <Text>Base URL: </Text>
          <TextInput value={baseURL} onChange={setBaseURL} />
        </Box>
        <Box>
          <Text>API Key: </Text>
          <TextInput value={apiKey} onChange={setApiKey} mask="*" />
        </Box>
        <Box>
          <Text>Model: </Text>
          <TextInput value={model} onChange={setModel} />
        </Box>

        {error && <Text color="red">{error}</Text>}

        <Box marginTop={1}>
          <Text color="gray">Press Enter to save</Text>
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
