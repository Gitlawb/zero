import React, { useState } from 'react';
import { Box, Text, useApp, useInput, useStdout } from 'ink';
import { theme } from './theme';
import { ProviderPicker } from './ProviderPicker';
import { ModelPicker } from './ModelPicker';
import { AddProvider } from './AddProvider';
import { ThemePicker } from './ThemePicker';
import { Logo } from './Logo';
import { ThinkingSpinner } from './Spinner';
import { MessageRenderer } from './MessageRenderer';
import { ToolCallRenderer } from './ToolCallRenderer';
import { setTheme, getAllThemes, getTheme } from './theme';
import { configManager } from '../config/manager';
import { loadProviderConfig } from '../config/provider';
import { createZeroProvider, resolveZeroProviderRuntime } from '../zero-provider-runtime';
import { runAgent } from '../agent/loop';
import {
  buildTuiModelStatus,
  formatModelListLines,
  resolveTuiModelSelection,
} from './model-selection';
import { detectFromColorFgBg } from './terminal-background';

type Screen = 'chat' | 'provider-picker' | 'add-provider' | 'model-picker' | 'theme-picker';

// Map low-level errors back to actionable guidance for the user. The full
// error object is still surfaced separately when debug mode is on.
function toFriendlyError(err: any): string {
  const raw = err?.message || String(err);
  const lower = raw.toLowerCase();

  if (lower.includes('no llm provider configured') || lower.includes('no provider')) {
    return 'No provider set up. Type /provider to add one.';
  }

  if (lower.includes('auth') || lower.includes('unauthorized') || lower.includes('invalid') || lower.includes('401') || lower.includes('api key')) {
    return `Authentication failed — check your API key. Type /provider to update it.\n(${raw})`;
  }

  if (lower.includes('rate') || lower.includes('quota')) {
    return `Provider rate limit or quota reached. Try again shortly.\n(${raw})`;
  }

  if (lower.includes('enotfound') || lower.includes('econnrefused') || lower.includes('etimedout') || lower.includes('fetch failed') || lower.includes('network')) {
    return `Network error reaching the provider. Check your connection and base URL.\n(${raw})`;
  }

  return `Error: ${raw}`;
}

type ChatMessage =
  | { type: 'user'; content: string }
  | { type: 'assistant'; content: string }
  | { type: 'tool-call'; name: string; args: string; result?: string }
  | { type: 'tool-result'; content: string } // legacy - results now attach to tool-call
  | { type: 'system'; content: string };

type SlashCommand = {
  name: string;
  description: string;
  aliases?: string[];
  run: (args: string[], rawCommand: string) => void;
};

function blend(a: string, b: string, t: number): string {
  const ah = parseInt(a.slice(1), 16);
  const bh = parseInt(b.slice(1), 16);
  const ar = (ah >> 16) & 0xff, ag = (ah >> 8) & 0xff, ab = ah & 0xff;
  const br = (bh >> 16) & 0xff, bg = (bh >> 8) & 0xff, bb = bh & 0xff;
  const rr = Math.round(ar + (br - ar) * t);
  const rg = Math.round(ag + (bg - ag) * t);
  const rb = Math.round(ab + (bb - ab) * t);
  return `#${((rr << 16) | (rg << 8) | rb).toString(16).padStart(6, '0')}`;
}

function isLightColor(color: string): boolean {
  const n = parseInt(color.slice(1), 16);
  const r = (n >> 16) & 0xff;
  const g = (n >> 8) & 0xff;
  const b = n & 0xff;
  return (r * 299 + g * 587 + b * 114) / 1000 > 128;
}

setTheme(configManager.getTheme());

interface AppProps {
  initialTerminalBackground?: string;
}

function truncateText(text: string, maxLength: number): string {
  if (maxLength <= 0) return '';
  if (text.length <= maxLength) return text;
  if (maxLength <= 3) return '';
  return `${text.slice(0, maxLength - 3)}...`;
}

function highlightSlashCommands(text: string, commands: SlashCommand[]): React.ReactNode {
  const commandNames = new Set(
    commands.flatMap((command) => [command.name, ...(command.aliases ?? [])]).map((name) => name.toLowerCase())
  );
  const parts: React.ReactNode[] = [];
  const regex = /\/[a-zA-Z][\w-]*/g;
  let lastIndex = 0;
  let match;

  while ((match = regex.exec(text)) !== null) {
    const value = match[0];
    if (!commandNames.has(value.toLowerCase())) continue;

    if (match.index > lastIndex) {
      parts.push(text.slice(lastIndex, match.index));
    }

    parts.push(
      <Text key={`${value}-${match.index}`} color={theme.text.accent}>
        {value}
      </Text>
    );
    lastIndex = match.index + value.length;
  }

  if (lastIndex < text.length) {
    parts.push(text.slice(lastIndex));
  }

  return parts.length > 0 ? <>{parts}</> : text;
}

function getSystemMessageTone(content: string): 'info' | 'success' | 'warning' | 'error' {
  const lower = content.toLowerCase();
  if (lower.includes('error') || lower.includes('failed') || lower.includes('authentication')) return 'error';
  if (lower.includes('no provider') || lower.includes('unknown') || lower.includes('not configured')) return 'warning';
  if (lower.includes('set to') || lower.includes('enabled') || lower.includes('disabled') || lower.includes('switched') || lower.includes('added')) return 'success';
  return 'info';
}

function renderSystemMessage(content: string, commands: SlashCommand[]): React.ReactNode {
  const lines = content.split('\n');
  const tone = getSystemMessageTone(content);
  const toneColor = tone === 'error'
    ? theme.status.error
    : tone === 'warning'
      ? theme.status.warning
      : tone === 'success'
        ? theme.status.success
        : theme.text.primary;

  return (
    <Box flexDirection="column">
      {lines.map((line, index) => (
        <Box key={`${index}-${line}`} flexDirection="row">
          <Text color={index === 0 ? toneColor : theme.text.primary}>
            {highlightSlashCommands(line, commands)}
          </Text>
        </Box>
      ))}
    </Box>
  );
}

export const App: React.FC<AppProps> = ({ initialTerminalBackground }) => {
  const { exit } = useApp();
  const { stdout } = useStdout();
  const columns = stdout?.columns ?? 80;
  const colorDepth = process.env.COLORTERM === 'truecolor' || process.env.COLORTERM === '24bit'
    ? 24
    : (process.env.TERM ?? '').includes('256color')
      ? 24
      : stdout?.getColorDepth?.() ?? 24;
  const [terminalBackground] = useState<string | undefined>(() => initialTerminalBackground ?? configManager.getTerminalBackground() ?? detectFromColorFgBg());
  const [screen, setScreen] = useState<Screen>('chat');
  const [input, setInput] = useState('');
  const [messages, setMessages] = useState<ChatMessage[]>([
    { type: 'system', content: 'Welcome to zero\nUse /help for available commands.' },
  ]);

  // Check on startup if we have any usable provider
  React.useEffect(() => {
    const checkProvider = async () => {
      try {
        await loadProviderConfig();
      } catch (err: any) {
        if (err.message?.includes('No LLM provider configured')) {
          setMessages((prev) => [
            ...prev,
            { 
              type: 'system', 
              content: 'No provider configured\nRun /provider to add one (OpenGateway recommended)'
            }
          ]);
        }
      }
    };
    checkProvider();
  }, []);
  const [isThinking, setIsThinking] = useState(false);
  const [streamingMessageIndex, setStreamingMessageIndex] = useState<number | null>(null);

  // Plan Mode (inspired by OpenClaude / Claude Code)
  const [isPlanMode, setIsPlanMode] = useState(false);
  const [selectedModelOverride, setSelectedModelOverride] = useState<string | undefined>();

  // Debug mode - when enabled, prints full error objects to console
  const [debugMode, setDebugMode] = useState(false);
  const [lastError, setLastError] = useState<any>(null);

  // Tools enabled (useful for debugging provider errors)
  const [toolsEnabled, setToolsEnabled] = useState(true);

  // Input box style
  const [inputStyle, setInputStyle] = useState<'border' | 'solid'>(configManager.getInputStyle());

  // Command suggestions (dropdown style)
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [suggestionIndex, setSuggestionIndex] = useState(0);
  const maxVisibleSuggestions = 6;

  // Input history for up/down arrow recall
  const [inputHistory, setInputHistory] = useState<string[]>([]);
  const [historyIndex, setHistoryIndex] = useState(-1);

  const slashCommandsRef = React.useRef<SlashCommand[]>([]);
  const slashCommands: SlashCommand[] = [
    {
      name: '/provider',
      description: 'Manage LLM providers',
      run: () => setScreen('provider-picker'),
    },
    {
      name: '/model',
      description: 'Select or list registry models',
      run: (args) => {
        const modelArg = args.join(' ').trim();

        if (!modelArg) {
          setScreen('model-picker');
          return;
        }

        if (modelArg.toLowerCase() === 'list') {
          setMessages((prev) => [
            ...prev,
            { type: 'system', content: ['Available models:', ...formatModelListLines().map((line) => `  ${line}`)].join('\n') },
          ]);
          return;
        }

        const selectedModel = resolveTuiModelSelection(modelArg);
        if (!selectedModel) {
          setMessages((prev) => [
            ...prev,
            { type: 'system', content: `Unknown model: ${modelArg}. Type /model list or /model to browse.` },
          ]);
          return;
        }

        setSelectedModelOverride(selectedModel.id);
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: `Model set for this session: ${selectedModel.displayName} (${selectedModel.provider})` },
        ]);
      },
    },
    {
      name: '/theme',
      description: 'Change the UI color theme',
      run: (args) => {
        const themeName = args.join(' ').trim();
        if (themeName) {
          const found = getAllThemes().find(t => t.name.toLowerCase() === themeName.toLowerCase());
          if (found) {
            setTheme(found.name);
            configManager.setTheme(found.name);
            setMessages((prev) => [...prev, { type: 'system', content: `Theme set to: ${found.name}` }]);
          } else {
            const names = getAllThemes().map(t => t.name).join(', ');
            setMessages((prev) => [...prev, { type: 'system', content: `Unknown theme. Available: ${names}` }]);
          }
        } else {
          setScreen('theme-picker');
        }
      },
    },
    {
      name: '/input-style',
      description: 'Toggle input border style',
      run: (args) => {
        const arg = args[0]?.toLowerCase();
        const next = arg === 'border' ? 'border' : arg === 'solid' ? 'solid' : inputStyle === 'border' ? 'solid' : 'border';
        setInputStyle(next);
        configManager.setInputStyle(next);
        setMessages((prev) => [...prev, { type: 'system', content: `Input style set to: ${next}` }]);
      },
    },
    {
      name: '/plan',
      description: 'Toggle Plan Mode',
      run: () => {
        setIsPlanMode(prev => {
          const next = !prev;
          setMessages((msgs) => [
            ...msgs,
            {
              type: 'system',
              content: next
                ? 'Plan mode enabled. The agent will focus on planning before making changes.'
                : 'Plan mode disabled.',
            },
          ]);
          return next;
        });
      },
    },
    {
      name: '/debug-mode',
      description: 'Toggle debug mode',
      aliases: ['/debug'],
      run: (args) => {
        const arg = args[0]?.toLowerCase();
        let nextDebug: boolean;

        if (arg === 'true') nextDebug = true;
        else if (arg === 'false') nextDebug = false;
        else nextDebug = !debugMode;

        setDebugMode(nextDebug);
        if (!nextDebug) setLastError(null);
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: `Debug mode ${nextDebug ? 'enabled' : 'disabled'}.` },
        ]);
      },
    },
    {
      name: '/tools',
      description: 'Toggle tool calling',
      run: (args) => {
        const arg = args[0]?.toLowerCase();
        let nextEnabled: boolean;

        if (arg === 'on' || arg === 'true') nextEnabled = true;
        else if (arg === 'off' || arg === 'false') nextEnabled = false;
        else nextEnabled = !toolsEnabled;

        setToolsEnabled(nextEnabled);
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: `Tool calling ${nextEnabled ? 'enabled' : 'disabled'}.` },
        ]);
      },
    },
    {
      name: '/help',
      description: 'Show available commands',
      run: () => {
        const commands = slashCommandsRef.current;
        const nameWidth = commands.reduce((max, command) => Math.max(max, command.name.length), 0);
        const helpLines = [
          'Available commands:',
          ...commands.map((command) => `  ${command.name.padEnd(nameWidth)} - ${command.description}`),
        ];
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: helpLines.join('\n') },
        ]);
      },
    },
    {
      name: '/exit',
      description: 'Quit zero',
      aliases: ['/quit'],
      run: () => exit(),
    },
  ];
  slashCommandsRef.current = slashCommands;

  // Update suggestions when input changes
  React.useEffect(() => {
    if (input.startsWith('/')) {
      const query = input.toLowerCase();
      const matches = slashCommands.map(command => command.name).filter(cmd => cmd.startsWith(query));
      setSuggestions(matches);
      setSuggestionIndex(0);
    } else {
      setSuggestions([]);
    }
  }, [input]);

  // Scrolling state (Grok Build style internal scrolling)
  const [scrollOffset, setScrollOffset] = useState(0);
  const [terminalRows, setTerminalRows] = useState(24); // default fallback

  // Current provider info for the footer. Do not fall back to the default model
  // unless an actual provider source exists.
  const effectiveProviderConfig = configManager.getEffectiveProviderConfig();
  const modelStatus = effectiveProviderConfig
    ? buildTuiModelStatus(effectiveProviderConfig, selectedModelOverride)
    : undefined;
  const currentModel = modelStatus
    ? `${modelStatus.label}${modelStatus.sourceLabel === 'session' ? ' *' : ''}`
    : 'No provider configured';
  const themeType = getTheme().type;
  const useTerminalBackground = Boolean(
    terminalBackground && (themeType === 'light' ? isLightColor(terminalBackground) : !isLightColor(terminalBackground))
  );
  const adaptedInputBackground = useTerminalBackground && terminalBackground
    ? blend(terminalBackground, theme.text.secondary, 0.24)
    : theme.background.input;
  const adaptedMessageBackground = useTerminalBackground && terminalBackground
    ? blend(terminalBackground, theme.text.secondary, 0.16)
    : theme.background.message;
  const solidInputBackground = colorDepth < 24
    ? (useTerminalBackground && terminalBackground ? terminalBackground : theme.background.primary)
    : adaptedInputBackground;

  // Track terminal size for proper scrolling
  React.useEffect(() => {
    const updateSize = () => {
      setTerminalRows(process.stdout.rows || 24);
    };
    process.stdout.on('resize', updateSize);
    updateSize();
    return () => {
      process.stdout.off('resize', updateSize);
    };
  }, []);

  // Auto-scroll to bottom when new messages arrive (unless user scrolled up)
  React.useEffect(() => {
    // Only auto-scroll if user is near the bottom
    if (scrollOffset <= 3) {
      setScrollOffset(0);
    }
  }, [messages.length]);

  // Only capture main chat input when we're actually in the chat screen
  const isInChat = screen === 'chat';

  useInput((inputChar, key) => {
    if (key.ctrl && inputChar === 'c') {
      exit();
      return;
    }

    // Don't process chat input while in provider picker or add flow
    if (!isInChat) return;

    if (key.upArrow && suggestions.length > 0) {
      setSuggestionIndex((prev) => prev <= 0 ? suggestions.length - 1 : prev - 1);
      return;
    }
    if (key.downArrow && suggestions.length > 0) {
      setSuggestionIndex((prev) => prev >= suggestions.length - 1 ? 0 : prev + 1);
      return;
    }
    if (key.upArrow && inputHistory.length > 0) {
      if (historyIndex === -1) {
        setHistoryIndex(inputHistory.length - 1);
        setInput(inputHistory[inputHistory.length - 1] ?? '');
      } else if (historyIndex > 0) {
        const newIdx = historyIndex - 1;
        setHistoryIndex(newIdx);
        setInput(inputHistory[newIdx] ?? '');
      }
      return;
    }
    if (key.downArrow && historyIndex !== -1) {
      if (historyIndex < inputHistory.length - 1) {
        const newIdx = historyIndex + 1;
        setHistoryIndex(newIdx);
        setInput(inputHistory[newIdx] ?? '');
      } else {
        setHistoryIndex(-1);
        setInput('');
      }
      return;
    }
    if (key.pageUp) {
      setScrollOffset((prev) => Math.min(prev + 8, messages.length - 1));
      return;
    }
    if (key.pageDown) {
      setScrollOffset((prev) => Math.max(prev - 8, 0));
      return;
    }
    if (key.home) {
      setScrollOffset(messages.length - 1);
      return;
    }
    if (key.end) {
      setScrollOffset(0);
      return;
    }

    if (key.return) {
      if (suggestions.length > 0) {
        const selected = (suggestions[suggestionIndex] + ' ').trim();
        setSuggestions([]);
        setInputHistory((prev) => prev[prev.length - 1] !== selected ? [...prev, selected] : prev);
        setHistoryIndex(-1);
        if (selected.startsWith('/')) {
          setInput('');
          setMessages((prev) => [...prev, { type: 'user', content: selected }]);
          handleSlashCommand(selected);
        } else {
          setInput(selected);
        }
      } else {
        handleSubmit();
      }
      return;
    }

    // Autocomplete first suggestion with Tab when typing a command
    if (key.tab && suggestions.length > 0) {
      setInput(suggestions[suggestionIndex] + ' ');
      setSuggestions([]);
      return;
    }

    if (key.backspace || key.delete) {
      setInput((prev) => prev.slice(0, -1));
      return;
    }

    if (inputChar && !key.ctrl && !key.meta) {
      setInput((prev) => prev + inputChar);
    }
  }, { isActive: isInChat });

  const handleSubmit = () => {
    if (!input.trim()) return;

    const trimmed = input.trim();
    setInput('');
    setSuggestions([]);

    // Save to input history (avoid duplicate consecutive entries)
    setInputHistory((prev) => prev[prev.length - 1] !== trimmed ? [...prev, trimmed] : prev);
    setHistoryIndex(-1);

    // Handle slash commands
    if (trimmed.startsWith('/')) {
      setMessages((prev) => [...prev, { type: 'user', content: trimmed }]);
      handleSlashCommand(trimmed);
      return;
    }

    // Regular message → send to agent
    setMessages((prev) => [...prev, { type: 'user', content: trimmed }]);

    const runAgentLoop = async () => {
      setIsThinking(true);

      try {
        const providerConfig = await loadProviderConfig();
        const runtime = resolveZeroProviderRuntime({
          provider: providerConfig.provider,
          apiKey: providerConfig.apiKey,
          baseURL: providerConfig.baseURL,
          model: selectedModelOverride || providerConfig.model,
          profileName: providerConfig.profileName,
          source: providerConfig.source,
        });
        const provider = createZeroProvider(runtime);

        // Add empty assistant message that we'll stream into
        setMessages((prev) => {
          const newMessages = [...prev, { type: 'assistant' as const, content: '' }];
          setStreamingMessageIndex(newMessages.length - 1);
          return newMessages;
        });

        await runAgent(trimmed, provider, {
          debug: debugMode,
          toolsEnabled,
          planMode: isPlanMode,
          onText: (text: string) => {
            setIsThinking(false);
            setMessages((prev) => {
              const newMessages = [...prev];
              const idx = streamingMessageIndex ?? newMessages.length - 1;

              if (newMessages[idx]?.type === 'assistant') {
                const current = newMessages[idx] as { type: 'assistant'; content: string };
                newMessages[idx] = {
                  ...current,
                  content: current.content + text,
                };
              }
              return newMessages;
            });
          },
          onToolCall: (tc) => {
            setIsThinking(false);
            setMessages((prev) => [
              ...prev,
              { type: 'tool-call', name: tc.name, args: tc.arguments },
            ]);
            // Reset streaming index since we inserted a message
            setStreamingMessageIndex(null);
          },
          onToolResult: (result) => {
            // Attach result to the most recent tool call that doesn't have one yet
            setMessages((prev) => {
              const newMessages = [...prev];
              for (let i = newMessages.length - 1; i >= 0; i--) {
                const msg = newMessages[i];
                if (msg && msg.type === 'tool-call' && (msg as any).result === undefined) {
                  (newMessages as any)[i] = {
                    ...msg,
                    result: result.result,
                  };
                  break;
                }
              }
              return newMessages;
            });
          },
        });
      } catch (err: any) {
        setIsThinking(false);

        if (debugMode) {
          setLastError(err);
          try {
            const red = '\x1b[31m';
            const reset = '\x1b[0m';
            const border = '─'.repeat(50);

            console.error(`\n${red}┌${border}┐`);
            console.error(`│  FULL PROVIDER ERROR${' '.repeat(29)}│`);
            console.error(`├${border}┤`);
            console.error(`│ Message: ${(err?.message || String(err)).slice(0, 40)}${' '.repeat(9)}│`);
            console.error(`│ Name:    ${err?.name || 'Error'}${' '.repeat(42 - (err?.name || 'Error').length)}│`);

            if (err?.response?.status) {
              console.error(`│ Status:  ${err.response.status}${' '.repeat(42 - String(err.response.status).length)}│`);
            }

            console.error(`└${border}┘${reset}`);
            console.error('Full object:');
            console.dir(err, { depth: 6 });
            console.error(`${red}${'='.repeat(52)}${reset}\n`);
          } catch (logErr) {
            console.error('Failed to log full error:', logErr);
          }
        } else {
          setLastError(null);
        }

        const friendlyMessage = toFriendlyError(err);
        setMessages((prev) => [...prev, { type: 'system', content: friendlyMessage }]);
      } finally {
        setIsThinking(false);
        setStreamingMessageIndex(null);
      }
    };

    runAgentLoop();
  };

  const handleSlashCommand = (command: string) => {
    const parts = command.trim().split(/\s+/);
    const cmd = parts[0]?.toLowerCase() ?? '';
    const slashCommand = slashCommandsRef.current.find(
      (candidate) => candidate.name === cmd || candidate.aliases?.includes(cmd)
    );

    if (slashCommand) {
      slashCommand.run(parts.slice(1), command);
      return;
    }

    setMessages((prev) => [...prev, { type: 'system', content: `Unknown command: ${command}` }]);
  };

  const handleProviderSelected = (name: string) => {
    const success = configManager.setActiveProvider(name);
    if (success) {
      setMessages((prev) => [...prev, { type: 'system', content: `Switched to provider: ${name}` }]);
      setSelectedModelOverride(undefined);
    }
    setScreen('chat');
  };

  const handleProviderPickerCancel = () => {
    setScreen('chat');
  };

  const handleModelSelected = (modelId: string) => {
    const selectedModel = resolveTuiModelSelection(modelId);
    setSelectedModelOverride(modelId);
    setMessages((prev) => [
      ...prev,
      {
        type: 'system',
        content: selectedModel
          ? `Model set for this session: ${selectedModel.displayName} (${selectedModel.provider})`
          : `Model set for this session: ${modelId}`,
      },
    ]);
    setScreen('chat');
  };

  const handleModelPickerCancel = () => {
    setScreen('chat');
  };

  const handleThemeSelected = (name: string) => {
    setTheme(name);
    configManager.setTheme(name);
    setMessages((prev) => [...prev, { type: 'system', content: `Theme set to: ${name}` }]);
    setScreen('chat');
  };

  const handleThemePickerCancel = () => {
    setScreen('chat');
  };

  const handleOpenAddProvider = () => {
    setScreen('add-provider');
  };

  const handleAddProviderDone = (providerName?: string) => {
    setScreen('chat');

    if (providerName) {
      // Automatically switch to the newly added provider
      const switched = configManager.setActiveProvider(providerName);

      if (switched) {
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: `Added and switched to provider: ${providerName}` },
        ]);
      } else {
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: `Provider added: ${providerName}` },
        ]);
      }
    } else {
      setMessages((prev) => [...prev, { type: 'system', content: 'Provider added successfully.' }]);
    }
  };

  const handleAddProviderCancel = () => {
    setScreen('provider-picker');
  };

  if (screen === 'add-provider') {
    return (
      <AddProvider
        onDone={handleAddProviderDone}
        onCancel={handleAddProviderCancel}
      />
    );
  }

  if (screen === 'provider-picker') {
    return (
      <ProviderPicker
        onSelect={handleProviderSelected}
        onCancel={handleProviderPickerCancel}
        onAddNew={handleOpenAddProvider}
      />
    );
  }

  if (screen === 'model-picker') {
    return (
      <ModelPicker
        activeModelId={modelStatus?.knownModel?.id || modelStatus?.modelId || selectedModelOverride}
        onSelect={handleModelSelected}
        onCancel={handleModelPickerCancel}
      />
    );
  }

  if (screen === 'theme-picker') {
    return (
      <ThemePicker
        onSelect={handleThemeSelected}
        onCancel={handleThemePickerCancel}
      />
    );
  }

  // Calculate visible messages for scrolling
  // scrollOffset: 0 = at bottom (newest), positive = scrolled up into history
  const chatHeight = Math.max(8, terminalRows - 6);
  const startIdx = Math.max(0, messages.length - chatHeight - scrollOffset);
  const visibleMessages = messages.slice(startIdx, startIdx + chatHeight);

  const hasOverflow = messages.length > chatHeight;
  const canScrollUp = hasOverflow && scrollOffset < messages.length - 1;
  const canScrollDown = hasOverflow && scrollOffset > 0;
  const suggestionWindowStart = suggestions.length > maxVisibleSuggestions
    ? Math.max(0, Math.min(suggestionIndex - 3, suggestions.length - maxVisibleSuggestions))
    : 0;
  const visibleSuggestions = suggestions.slice(suggestionWindowStart, suggestionWindowStart + maxVisibleSuggestions);
  const suggestionNameWidth = suggestions.reduce((max, cmd) => {
    const display = cmd.startsWith('/') ? cmd.slice(1) : cmd;
    return Math.max(max, display.length);
  }, 0);
  const suggestionDescriptionWidth = Math.max(0, columns - suggestionNameWidth - 8);

  return (
    <Box flexDirection="column" height="100%">
      {/* Scrollable messages area with right-side scroll indicator (Grok Build style) */}
      <Box 
        flexGrow={1} 
        flexDirection="row"
        overflow="hidden"
      >
        {/* Main chat content */}
        <Box 
          flexGrow={1} 
          flexDirection="column" 
          paddingX={1} 
          paddingTop={1}
        >
        <Logo />

        {(canScrollUp || canScrollDown) && (
          <Text color={theme.ui.comment}>
            {canScrollUp ? '↑ ' : '  '}Scroll PgUp/PgDn / Home/End {canScrollDown ? '↓' : ''}
          </Text>
        )}

        <Box flexDirection="column">
          {visibleMessages.map((msg, index) => {
            const realIndex = startIdx + index;

            if (msg.type === 'user') {
              const messageWidth = Math.max(1, columns - 2);
              return (
                <Box key={realIndex} flexDirection="column" width="100%" marginBottom={1}>
                  <Box width="100%" height={1}>
                    <Text color={adaptedMessageBackground}>{'▄'.repeat(messageWidth)}</Text>
                  </Box>
                  <Box paddingX={1} backgroundColor={adaptedMessageBackground} flexDirection="row" width="100%">
                    <Text color={theme.text.accent} backgroundColor={adaptedMessageBackground}>{'> '}</Text>
                    <Text color={theme.text.accent} backgroundColor={adaptedMessageBackground}>{msg.content}</Text>
                  </Box>
                  <Box width="100%" height={1}>
                    <Text color={adaptedMessageBackground}>{'▀'.repeat(messageWidth)}</Text>
                  </Box>
                </Box>
              );
            }

            if (msg.type === 'assistant') {
              const isStreaming = realIndex === streamingMessageIndex;
              return (
                <Box key={realIndex} marginBottom={1} flexDirection="row">
                  <Text color={theme.ui.active} bold>{'⛬ '}</Text>
                  <Box flexDirection="column" flexGrow={1}>
                    <MessageRenderer content={msg.content} />
                    {isStreaming && (
                      <Text color={theme.ui.active} bold>▌</Text>
                    )}
                  </Box>
                </Box>
              );
            }

            if (msg.type === 'tool-call') {
              const hasResult = !!msg.result;
              return (
                <Box key={realIndex} marginBottom={0}>
                  <ToolCallRenderer
                    name={msg.name}
                    args={msg.args}
                    result={msg.result}
                    status={hasResult ? 'success' : 'running'}
                  />
                </Box>
              );
            }

            if (msg.type === 'tool-result') {
              // Legacy separate results are no longer created; ignore for cleanliness
              return null;
            }

            return (
              <Box key={realIndex} marginBottom={1}>
                {renderSystemMessage(msg.content, slashCommands)}
              </Box>
            );
          })}

          {isThinking && <ThinkingSpinner />}
        </Box>
        </Box>
      </Box>

      {canScrollUp && (
        <Box paddingX={1} justifyContent="flex-end">
          <Text color={theme.ui.comment}>
            ↑{scrollOffset}{canScrollDown ? ' ↓' : ''}
          </Text>
        </Box>
      )}

      {debugMode && lastError && (
        <Box 
          borderStyle="single" 
          borderColor={theme.status.error}
          paddingX={1} 
        paddingY={1}
          marginBottom={1}
        >
          <Text color={theme.status.error} bold>⚠ Debug Error</Text>
          <Text color={theme.ui.comment}>
            {lastError.message || String(lastError)}
          </Text>
          {lastError.stack && (
          <Text color={theme.ui.comment}>
              {lastError.stack.split('\n').slice(0, 8).join('\n')}
            </Text>
          )}
          <Text color={theme.text.secondary}>
            (Full details in terminal • /debug-mode false to hide)
          </Text>
        </Box>
      )}

      {/* Solid mode top separator */}
      {inputStyle === 'solid' && (
        <Box width="100%" height={1}>
          <Text color={solidInputBackground}>{'▄'.repeat(columns)}</Text>
        </Box>
      )}

      {/* Input box at the bottom */}
      <Box
        borderStyle={inputStyle === 'border' ? 'round' : undefined}
        borderColor={isPlanMode ? theme.status.success : theme.ui.active}
        backgroundColor={inputStyle === 'solid' ? solidInputBackground : undefined}
        paddingX={1}
        paddingY={0}
        flexDirection="row"
        alignItems="center"
      >
        {/* Left: prompt + input */}
        <Box flexDirection="row">
          <Text color={isPlanMode ? theme.status.success : theme.text.accent}>{'> '}</Text>
          {input ? (
            <>
              <Text color={theme.text.primary}>{input}</Text>
              <Text color={theme.text.secondary}>█</Text>
            </>
          ) : (
            <>
              <Text color={theme.text.secondary}>█ </Text>
              <Text color={theme.text.secondary} wrap="truncate">Type your message or @path/to/file</Text>
            </>
          )}
        </Box>
      </Box>

      {/* Solid mode bottom separator */}
      {inputStyle === 'solid' && (
        <Box width="100%" height={1}>
          <Text color={solidInputBackground}>{'▀'.repeat(columns)}</Text>
        </Box>
      )}

      {suggestions.length > 0 && (
        <Box flexDirection="column" paddingLeft={3}>
          {visibleSuggestions.map((s, i) => {
            const realIndex = suggestionWindowStart + i;
            const isFocused = realIndex === suggestionIndex;
            const rowColor = isFocused ? theme.text.primary : theme.text.secondary;
            const display = s.startsWith('/') ? s.slice(1) : s;
            const commandMeta = slashCommands.find((command) => command.name === s);
            const description = truncateText(commandMeta?.description ?? '', suggestionDescriptionWidth);
            const descriptionPadding = ' '.repeat(Math.max(1, suggestionNameWidth - display.length + 2));
            return (
              <Box key={s} flexDirection="row">
                <Text color={rowColor} bold={isFocused}>
                  {display}
                </Text>
                {description ? (
                  <Text color={rowColor} bold={isFocused}>
                    {descriptionPadding}{description}
                  </Text>
                ) : null}
              </Box>
            );
          })}
        </Box>
      )}

      {suggestions.length === 0 && (
        <Box paddingX={1} flexDirection="row">
          <Text color={modelStatus ? theme.text.secondary : theme.status.warning}>
            {modelStatus ? `${currentModel} Model` : currentModel}
          </Text>
          {isPlanMode && (
            <Text color={theme.status.success}> · PLAN MODE</Text>
          )}
        </Box>
      )}
    </Box>
  );
};
