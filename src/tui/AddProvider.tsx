import React, { useState } from 'react';
import { Box, Text, useInput } from 'ink';
import TextInput from 'ink-text-input';
import { configManager } from '../config/manager';
import { tuiTheme } from './theme';

type AddMode = 'choose' | 'opengateway' | 'generic';

interface AddProviderProps {
  onDone: (providerName?: string) => void;
  onCancel: () => void;
}

export const AddProvider: React.FC<AddProviderProps> = ({ onDone, onCancel }) => {
  const [mode, setMode] = useState<AddMode>('choose');
  const [selectedOption, setSelectedOption] = useState(0);
  const [openGatewayStep, setOpenGatewayStep] = useState(0);
  const [genericStep, setGenericStep] = useState(0);
  const [openGatewayKey, setOpenGatewayKey] = useState('');
  const [openGatewayModel, setOpenGatewayModel] = useState('mimo-v2.5-pro');
  const [name, setName] = useState('');
  const [baseURL, setBaseURL] = useState('https://api.openai.com/v1');
  const [apiKey, setApiKey] = useState('');
  const [model, setModel] = useState('gpt-4.1');
  const [error, setError] = useState('');
  const [successMessage, setSuccessMessage] = useState('');

  useInput((input, key) => {
    if (key.escape) {
      if (mode === 'choose') {
        onCancel();
      } else {
        resetToChoose();
      }
      return;
    }

    if (mode !== 'choose') return;

    if (key.upArrow) {
      setSelectedOption((prev) => Math.max(0, prev - 1));
      return;
    }

    if (key.downArrow) {
      setSelectedOption((prev) => Math.min(1, prev + 1));
      return;
    }

    if (key.return) {
      setMode(selectedOption === 0 ? 'opengateway' : 'generic');
      return;
    }

    if (input === '1') setMode('opengateway');
    if (input === '2') setMode('generic');
  });

  const resetToChoose = () => {
    setMode('choose');
    setSelectedOption(0);
    setOpenGatewayStep(0);
    setGenericStep(0);
    setError('');
    setSuccessMessage('');
  };

  const saveOpenGateway = () => {
    if (!openGatewayKey.trim()) {
      setError('API key is required.');
      return;
    }

    const profileName = 'opengateway';
    configManager.addProvider({
      name: profileName,
      baseURL: 'https://opengateway.gitlawb.com/v1',
      apiKey: openGatewayKey.trim(),
      model: openGatewayModel.trim(),
      description: 'OpenGateway',
    });

    setSuccessMessage('OpenGateway provider saved.');
    setTimeout(() => onDone(profileName), 900);
  };

  const saveGeneric = () => {
    if (!name.trim() || !baseURL.trim() || !model.trim()) {
      setError('Name, base URL, and model are required.');
      return;
    }

    configManager.addProvider({
      name: name.trim(),
      baseURL: baseURL.trim(),
      apiKey: apiKey.trim() || undefined,
      model: model.trim(),
      description: 'Custom OpenAI-compatible',
    });

    setSuccessMessage('Provider saved.');
    setTimeout(() => onDone(name.trim()), 900);
  };

  if (successMessage) {
    return (
      <Box flexDirection="column" padding={1}>
        <Text color={tuiTheme.colors.success} bold>{successMessage}</Text>
        <Text color={tuiTheme.colors.muted} dimColor>Returning to chat.</Text>
      </Box>
    );
  }

  if (mode === 'choose') {
    return (
      <Box flexDirection="column" padding={1}>
        <Text bold color={tuiTheme.colors.brand}>Add Provider</Text>
        <Text color={tuiTheme.colors.muted} dimColor>
          Up/Down moves, Enter selects, Esc returns
        </Text>

        <Box marginY={1} flexDirection="column">
          <ProviderOption
            index={1}
            label="OpenGateway"
            description="Use the hosted OpenAI-compatible gateway."
            selected={selectedOption === 0}
          />
          <ProviderOption
            index={2}
            label="Custom OpenAI-compatible"
            description="Use OpenAI, Groq, Ollama, OpenRouter, or another compatible endpoint."
            selected={selectedOption === 1}
          />
        </Box>
      </Box>
    );
  }

  if (mode === 'opengateway') {
    return (
      <ProviderForm title="Add OpenGateway Provider" error={error}>
        {openGatewayStep === 0 ? (
          <Field label="API key" helper="Get one from https://opengateway.gitlawb.com">
            <TextInput
              value={openGatewayKey}
              onChange={setOpenGatewayKey}
              onSubmit={() => {
                if (!openGatewayKey.trim()) {
                  setError('API key cannot be empty.');
                  return;
                }
                setError('');
                setOpenGatewayStep(1);
              }}
              mask="*"
              placeholder="ogw_live_..."
            />
          </Field>
        ) : (
          <Field label="Model" helper="Press Enter to save this provider.">
            <TextInput
              value={openGatewayModel}
              onChange={setOpenGatewayModel}
              onSubmit={saveOpenGateway}
            />
          </Field>
        )}
      </ProviderForm>
    );
  }

  return (
    <ProviderForm title="Add Custom Provider" error={error}>
      {genericStep === 0 && (
        <Field label="Name" helper="A short local name for this profile.">
          <TextInput
            value={name}
            onChange={setName}
            onSubmit={() => {
              if (!name.trim()) {
                setError('Name is required.');
                return;
              }
              setError('');
              setGenericStep(1);
            }}
            placeholder="work"
          />
        </Field>
      )}

      {genericStep === 1 && (
        <Field label="Base URL" helper="OpenAI-compatible API base URL.">
          <TextInput
            value={baseURL}
            onChange={setBaseURL}
            onSubmit={() => {
              if (!baseURL.trim()) {
                setError('Base URL is required.');
                return;
              }
              setError('');
              setGenericStep(2);
            }}
          />
        </Field>
      )}

      {genericStep === 2 && (
        <Field label="API key" helper="Leave blank only for local gateways that do not need one.">
          <TextInput
            value={apiKey}
            onChange={setApiKey}
            onSubmit={() => {
              setError('');
              setGenericStep(3);
            }}
            mask="*"
          />
        </Field>
      )}

      {genericStep === 3 && (
        <Field label="Model" helper="Press Enter to save this provider.">
          <TextInput value={model} onChange={setModel} onSubmit={saveGeneric} />
        </Field>
      )}
    </ProviderForm>
  );
};

function ProviderOption({
  index,
  label,
  description,
  selected,
}: {
  index: number;
  label: string;
  description: string;
  selected: boolean;
}) {
  return (
    <Box flexDirection="column" paddingLeft={1} marginBottom={1}>
      <Text color={selected ? tuiTheme.colors.accent : tuiTheme.colors.text}>
        {selected ? '> ' : '  '}
        {index}. {label}
      </Text>
      {selected && (
        <Text color={tuiTheme.colors.muted} dimColor>
          {'   '}{description}
        </Text>
      )}
    </Box>
  );
}

function ProviderForm({
  title,
  error,
  children,
}: {
  title: string;
  error: string;
  children: React.ReactNode;
}) {
  return (
    <Box flexDirection="column" padding={1}>
      <Text bold color={tuiTheme.colors.brand}>{title}</Text>
      <Text color={tuiTheme.colors.muted} dimColor>Esc returns to provider choices.</Text>
      <Box marginTop={1} flexDirection="column">
        {children}
      </Box>
      {error && (
        <Box marginTop={1}>
          <Text color={tuiTheme.colors.danger}>{error}</Text>
        </Box>
      )}
    </Box>
  );
}

function Field({
  label,
  helper,
  children,
}: {
  label: string;
  helper: string;
  children: React.ReactNode;
}) {
  return (
    <Box flexDirection="column">
      <Text color={tuiTheme.colors.text}>{label}</Text>
      <Text color={tuiTheme.colors.muted} dimColor>{helper}</Text>
      <Box marginTop={1}>
        {children}
      </Box>
    </Box>
  );
}
