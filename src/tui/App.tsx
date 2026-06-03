import React, { useRef, useState } from 'react';
import { useApp, useInput, useStdout } from 'ink';
import { runAgent, type ToolApprovalDecision, type ToolApprovalRequest } from '../agent/loop';
import { configManager } from '../config/manager';
import { redactZeroError, redactZeroSecrets, redactZeroString } from '../zero-redaction';
import { formatZeroConfigInspection, inspectZeroConfig, type ZeroConfigInspectionReport } from '../zero-config-inspection';
import { formatZeroDoctorReport, runZeroDoctor, type ZeroDoctorReport } from '../zero-doctor';
import { formatZeroSearchResult, searchZeroSessions } from '../zero-search';
import { createZeroRunContext } from '../zero-runtime';
import { AddProvider } from './AddProvider';
import { ModelPicker } from './ModelPicker';
import { ProviderPicker } from './ProviderPicker';
import { ThemePicker } from './ThemePicker';
import { TuiShell } from './TuiShell';
import {
  formatTuiHelpLines,
  listTuiCommands,
} from './commands';
import {
  buildTuiModelStatus,
  formatModelListLines,
  formatModelProfileLines,
  resolveTuiModelProfileSelection,
  resolveTuiModelSelection,
} from './model-selection';
import { getAllThemes, getTheme, normalizeHexColor, setTheme } from './theme';
import { detectFromColorFgBg } from './terminal-background';
import type { ChatMessage } from './types';

type Screen = 'chat' | 'provider-picker' | 'add-provider' | 'model-picker' | 'theme-picker';
const KNOWN_COMMANDS = listTuiCommands().map((command) => command.name);
const hasInitialProvider = Boolean(process.env.ZERO_PROVIDER_COMMAND || configManager.getEffectiveProviderConfig());
const INITIAL_MESSAGES: ChatMessage[] = [
  { type: 'system', content: 'Welcome to zero\nUse /help for available commands.' },
  ...(!hasInitialProvider
    ? [{ type: 'system' as const, content: 'No provider configured\nRun /provider to add one (OpenGateway recommended)' }]
    : []),
];

setTheme(configManager.getTheme());

function blend(a: string, b: string, t: number): string {
  const ah = parseInt(normalizeHexColor(a).slice(1), 16);
  const bh = parseInt(normalizeHexColor(b).slice(1), 16);
  const ar = (ah >> 16) & 0xff, ag = (ah >> 8) & 0xff, ab = ah & 0xff;
  const br = (bh >> 16) & 0xff, bg = (bh >> 8) & 0xff, bb = bh & 0xff;
  const rr = Math.round(ar + (br - ar) * t);
  const rg = Math.round(ag + (bg - ag) * t);
  const rb = Math.round(ab + (bb - ab) * t);
  return `#${((rr << 16) | (rg << 8) | rb).toString(16).padStart(6, '0')}`;
}

function isLightColor(color: string): boolean {
  const n = parseInt(normalizeHexColor(color).slice(1), 16);
  const r = (n >> 16) & 0xff;
  const g = (n >> 8) & 0xff;
  const b = n & 0xff;
  return (r * 299 + g * 587 + b * 114) / 1000 > 128;
}

interface AppProps {
  initialTerminalBackground?: string;
}

export const App: React.FC<AppProps> = ({ initialTerminalBackground }) => {
  const { exit } = useApp();
  const { stdout } = useStdout();
  const columns = stdout?.columns ?? process.stdout.columns ?? 80;
  const rows = stdout?.rows ?? process.stdout.rows ?? 24;
  const colorDepth = process.env.COLORTERM === 'truecolor' || process.env.COLORTERM === '24bit'
    ? 24
    : (process.env.TERM ?? '').includes('256color')
      ? 8
      : stdout?.getColorDepth?.() ?? process.stdout.getColorDepth?.() ?? 24;
  const [terminalBackground] = useState<string | undefined>(() => (
    initialTerminalBackground ?? configManager.getTerminalBackground() ?? detectFromColorFgBg()
  ));
  const [screen, setScreen] = useState<Screen>('chat');
  const [input, setInput] = useState('');
  const [messages, setMessages] = useState<ChatMessage[]>(INITIAL_MESSAGES);
  const [isThinking, setIsThinking] = useState(false);
  const [streamingMessageIndex, setStreamingMessageIndex] = useState<number | null>(null);
  const streamingMessageIndexRef = useRef<number | null>(null);
  const [isPlanMode, setIsPlanMode] = useState(false);
  const [selectedModelOverride, setSelectedModelOverride] = useState<string | undefined>();
  const [debugMode, setDebugMode] = useState(false);
  const [lastError, setLastError] = useState<any>(null);
  const [toolsEnabled, setToolsEnabled] = useState(true);
  const [inputStyle, setInputStyle] = useState<'border' | 'solid'>(configManager.getInputStyle());
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [suggestionIndex, setSuggestionIndex] = useState(0);
  const [inputHistory, setInputHistory] = useState<string[]>([]);
  const [historyIndex, setHistoryIndex] = useState(-1);
  const [scrollOffset, setScrollOffset] = useState(0);
  const [pendingApproval, setPendingApproval] = useState<ToolApprovalRequest | null>(null);
  const approvalResolverRef = useRef<((decision: ToolApprovalDecision) => void) | null>(null);
  const approvalGrantsRef = useRef(new Set<string>());

  React.useEffect(() => {
    if (!input.startsWith('/')) {
      setSuggestions([]);
      return;
    }

    const query = input.toLowerCase();
    setSuggestions(KNOWN_COMMANDS.filter((cmd) => cmd.startsWith(query)));
    setSuggestionIndex(0);
  }, [input]);

  React.useEffect(() => {
    if (scrollOffset <= 3) {
      setScrollOffset(0);
    }
  }, [messages.length]);

  const effectiveProviderConfig = configManager.getEffectiveProviderConfig();
  const modelStatus = effectiveProviderConfig
    ? buildTuiModelStatus(effectiveProviderConfig, selectedModelOverride)
    : undefined;
  const currentProviderName = modelStatus?.providerLabel || 'No provider';
  const currentModel = modelStatus
    ? `${modelStatus.label}${modelStatus.sourceLabel === 'session' ? ' *' : ''}`
    : 'No provider configured';
  const activeTheme = getTheme();
  const useTerminalBackground = Boolean(
    terminalBackground && (activeTheme.type === 'light' ? isLightColor(terminalBackground) : !isLightColor(terminalBackground))
  );
  const adaptedInputBackground = useTerminalBackground && terminalBackground
    ? blend(terminalBackground, activeTheme.colors.text.secondary, 0.24)
    : activeTheme.colors.background.input;
  const adaptedMessageBackground = useTerminalBackground && terminalBackground
    ? blend(terminalBackground, activeTheme.colors.text.secondary, 0.16)
    : activeTheme.colors.background.message;
  const solidInputBackground = colorDepth < 24
    ? (useTerminalBackground && terminalBackground ? terminalBackground : activeTheme.colors.background.primary)
    : adaptedInputBackground;
  const isInChat = screen === 'chat';

  const rememberInput = (value: string) => {
    setInputHistory((prev) => prev[prev.length - 1] !== value ? [...prev, value] : prev);
    setHistoryIndex(-1);
  };

  useInput((inputChar, key) => {
    if (key.ctrl && inputChar === 'c') {
      exit();
      return;
    }

    if (pendingApproval) {
      const decision = inputChar?.toLowerCase();
      if (decision === 'y') {
        resolvePendingApproval('allow');
      } else if (decision === 'n') {
        resolvePendingApproval('deny');
      } else if (decision === 'a') {
        approvalGrantsRef.current.add(pendingApproval.grantKey);
        resolvePendingApproval('allow-session');
      }
      return;
    }

    if (!isInChat) return;

    if (suggestions.length > 0) {
      if (key.upArrow) {
        setSuggestionIndex((prev) => prev <= 0 ? suggestions.length - 1 : prev - 1);
        return;
      }
      if (key.downArrow) {
        setSuggestionIndex((prev) => prev >= suggestions.length - 1 ? 0 : prev + 1);
        return;
      }
    }

    if (key.upArrow && inputHistory.length > 0) {
      if (historyIndex === -1) {
        setHistoryIndex(inputHistory.length - 1);
        setInput(inputHistory[inputHistory.length - 1] ?? '');
      } else if (historyIndex > 0) {
        const nextIndex = historyIndex - 1;
        setHistoryIndex(nextIndex);
        setInput(inputHistory[nextIndex] ?? '');
      }
      return;
    }

    if (key.downArrow && historyIndex !== -1) {
      if (historyIndex < inputHistory.length - 1) {
        const nextIndex = historyIndex + 1;
        setHistoryIndex(nextIndex);
        setInput(inputHistory[nextIndex] ?? '');
      } else {
        setHistoryIndex(-1);
        setInput('');
      }
      return;
    }

    if (!input) {
      const currentTerminalHeight = Math.max(20, rows);
      const currentChatHeight = Math.max(8, currentTerminalHeight - 6);
      const currentMaxScrollOffset = Math.max(0, messages.length - currentChatHeight);

      if (key.upArrow) {
        setScrollOffset((prev) => Math.min(prev + 1, currentMaxScrollOffset));
        return;
      }
      if (key.downArrow) {
        setScrollOffset((prev) => Math.max(prev - 1, 0));
        return;
      }
      if (key.pageUp) {
        setScrollOffset((prev) => Math.min(prev + 8, currentMaxScrollOffset));
        return;
      }
      if (key.pageDown) {
        setScrollOffset((prev) => Math.max(prev - 8, 0));
        return;
      }
      if (key.home) {
        setScrollOffset(currentMaxScrollOffset);
        return;
      }
      if (key.end) {
        setScrollOffset(0);
        return;
      }
    }

    if (key.return) {
      if (suggestions.length > 0) {
        const selected = suggestions[suggestionIndex] ?? suggestions[0];
        setInput('');
        setSuggestions([]);
        if (selected) {
          rememberInput(selected);
          addMessage({ type: 'user', content: selected });
          void handleSlashCommand(selected);
        }
        return;
      }
      handleSubmit();
      return;
    }

    if (key.tab && suggestions.length > 0) {
      setInput(`${suggestions[suggestionIndex] ?? suggestions[0]} `);
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
    const trimmed = input.trim();
    if (!trimmed) return;

    setInput('');
    setSuggestions([]);
    streamingMessageIndexRef.current = null;
    setStreamingMessageIndex(null);
    rememberInput(trimmed);

    addMessage({ type: 'user', content: trimmed });

    if (trimmed.startsWith('/')) {
      void handleSlashCommand(trimmed);
      return;
    }

    void runAgentLoop(trimmed);
  };

  const runAgentLoop = async (prompt: string) => {
    setIsThinking(true);

    try {
      const context = await createZeroRunContext({
        surface: 'tui',
        model: selectedModelOverride,
        permissionMode: 'ask',
      });

      await runAgent(prompt, context.provider, {
        ...context.agentOptions,
        debug: debugMode,
        toolsEnabled,
        planMode: isPlanMode,
        onToolApproval: requestToolApproval,
        onText: appendAssistantText,
        onToolCall: (tc) => {
          setIsThinking(false);
          streamingMessageIndexRef.current = null;
          setStreamingMessageIndex(null);
          addMessage({ type: 'tool-call', id: tc.id, name: tc.name, args: redactZeroString(tc.arguments) });
        },
        onToolResult: (result) => {
          setMessages((prev) => prev.map((msg) => (
            msg.type === 'tool-call' && msg.id === result.toolCallId
              ? { ...msg, result: redactZeroString(result.result) }
              : msg
          )));
        },
      });
    } catch (err: any) {
      setIsThinking(false);
      const safeError = redactZeroError(err);
      if (debugMode) {
        setLastError(safeError);
        logDebugError(safeError);
      } else {
        setLastError(null);
      }
      addSystemMessage(toFriendlyError(err));
    } finally {
      setIsThinking(false);
      streamingMessageIndexRef.current = null;
      setStreamingMessageIndex(null);
    }
  };

  const appendAssistantText = (text: string) => {
    setIsThinking(false);
    setMessages((prev) => {
      const next = [...prev];
      let index = streamingMessageIndexRef.current;

      if (index === null || next[index]?.type !== 'assistant') {
        const lastIndex = next.length - 1;
        index = next[lastIndex]?.type === 'assistant' ? lastIndex : next.length;
        if (index === next.length) {
          next.push({ type: 'assistant', content: '' });
        }
        streamingMessageIndexRef.current = index;
        setStreamingMessageIndex(index);
      }

      const current = next[index];
      if (current?.type === 'assistant') {
        next[index] = { ...current, content: current.content + text };
      }

      return next;
    });
  };

  const handleSlashCommand = async (command: string) => {
    const parts = command.trim().split(/\s+/);
    const cmd = parts[0]?.toLowerCase() ?? '';
    const arg = parts[1]?.toLowerCase();

    if (cmd === '/provider') {
      setScreen('provider-picker');
      return;
    }

    if (cmd === '/model') {
      handleModelCommand(parts.slice(1).join(' ').trim());
      return;
    }

    if (cmd === '/theme') {
      const themeName = parts.slice(1).join(' ').trim();
      if (!themeName) {
        setScreen('theme-picker');
        return;
      }

      const found = getAllThemes().find((item) => item.name.toLowerCase() === themeName.toLowerCase());
      if (!found) {
        const names = getAllThemes().map((item) => item.name).join(', ');
        addSystemMessage(`Unknown theme. Available: ${names}`);
        return;
      }

      setTheme(found.name);
      configManager.setTheme(found.name);
      addSystemMessage(`Theme set to: ${found.name}`);
      return;
    }

    if (cmd === '/input-style') {
      const value = parts[1]?.toLowerCase();
      if (value && value !== 'border' && value !== 'solid') {
        addSystemMessage('Usage: /input-style [border|solid]');
        return;
      }

      const next: 'border' | 'solid' = value === 'border' || value === 'solid'
        ? value
        : inputStyle === 'border'
          ? 'solid'
          : 'border';
      setInputStyle(next);
      configManager.setInputStyle(next);
      addSystemMessage(`Input style set to: ${next}`);
      return;
    }

    if (cmd === '/clear') {
      setMessages(INITIAL_MESSAGES);
      setScrollOffset(0);
      return;
    }

    if (cmd === '/context') {
      addMessages([
        { type: 'system', content: 'Context:' },
        { type: 'system', content: `  provider      ${currentProviderName}` },
        { type: 'system', content: `  model         ${currentModel}` },
        { type: 'system', content: `  mode          ${isPlanMode ? 'plan' : 'chat'}` },
        { type: 'system', content: `  permissions   ask (${approvalGrantsRef.current.size} session grants)` },
        { type: 'system', content: `  tools         ${toolsEnabled ? 'enabled' : 'disabled'}` },
        { type: 'system', content: `  tokens        ${estimatedTokens} estimated (${contextPercent}%)` },
        { type: 'system', content: `  cost          $${estimatedCost.toFixed(4)} estimated` },
        { type: 'system', content: `  active file   ${activeFile ?? 'none'}` },
      ]);
      return;
    }

    if (cmd === '/plan') {
      setIsPlanMode((prev) => {
        const next = !prev;
        addSystemMessage(next
          ? 'Plan mode enabled. The agent will focus on planning before making changes.'
          : 'Plan mode disabled.');
        return next;
      });
      return;
    }

    if (cmd === '/debug-mode' || cmd === '/debug') {
      const nextDebug = arg === 'true'
        ? true
        : arg === 'false'
          ? false
          : !debugMode;

      setDebugMode(nextDebug);
      if (!nextDebug) setLastError(null);
      addSystemMessage(`Debug mode ${nextDebug ? 'enabled' : 'disabled'}.`);
      return;
    }

    if (cmd === '/tools') {
      const arg2 = parts[1]?.toLowerCase();
      const nextEnabled = arg2 === 'on' || arg2 === 'true'
        ? true
        : arg2 === 'off' || arg2 === 'false'
          ? false
          : !toolsEnabled;

      setToolsEnabled(nextEnabled);
      addSystemMessage(`Tool calling ${nextEnabled ? 'enabled' : 'disabled'}.`);
      return;
    }

    if (cmd === '/permissions') {
      addMessages([
        { type: 'system', content: 'Permissions:' },
        { type: 'system', content: '  mode          ask before shell/write/patch tools' },
        { type: 'system', content: `  tools         ${toolsEnabled ? 'enabled' : 'disabled'}` },
        { type: 'system', content: `  session grants ${approvalGrantsRef.current.size}` },
        { type: 'system', content: '  controls      y allow, n deny, a allow this session' },
      ]);
      return;
    }

    if (cmd === '/doctor') {
      await runTuiDoctor(parts.slice(1));
      return;
    }

    if (cmd === '/config') {
      runTuiConfigInspection();
      return;
    }

    if (cmd === '/search') {
      await runTuiSearch(parts.slice(1).join(' ').trim());
      return;
    }

    if (cmd === '/help') {
      addMessages([
        { type: 'system', content: 'Available commands:' },
        ...formatTuiHelpLines().map((line) => ({ type: 'system' as const, content: `  ${line}` })),
      ]);
      return;
    }

    if (cmd === '/exit' || cmd === '/quit') {
      exit();
      return;
    }

    addSystemMessage(`Unknown command: ${command}`);
  };

  const handleModelCommand = (modelArg: string) => {
    if (!modelArg) {
      setScreen('model-picker');
      return;
    }

    if (modelArg.toLowerCase() === 'list') {
      addMessages([
        { type: 'system', content: 'Available models:' },
        ...formatModelListLines().map((line) => ({ type: 'system' as const, content: `  ${line}` })),
      ]);
      return;
    }

    if (modelArg.toLowerCase() === 'profiles') {
      addMessages([
        { type: 'system', content: 'Model profiles:' },
        ...formatModelProfileLines().map((line) => ({ type: 'system' as const, content: `  ${line}` })),
      ]);
      return;
    }

    const profileSelection = resolveTuiModelProfileSelection(modelArg);
    if (profileSelection) {
      setSelectedModelOverride(profileSelection.model.id);
      addSystemMessage(
        `Model profile ${profileSelection.profile.id} selected: ` +
        `${profileSelection.model.displayName} (${profileSelection.model.provider})`
      );
      return;
    }

    const selectedModel = resolveTuiModelSelection(modelArg);
    if (!selectedModel) {
      addSystemMessage(`Unknown model: ${modelArg}. Type /model list or /model to browse.`);
      return;
    }

    setSelectedModelOverride(selectedModel.id);
    addSystemMessage(`Model set for this session: ${selectedModel.displayName} (${selectedModel.provider})`);
  };

  const runTuiDoctor = async (args: string[]) => {
    addSystemMessage('Running Zero doctor...');
    try {
      const report = await runZeroDoctor({
        connectivity: args.includes('--connectivity'),
      });
      addSystemLines(formatZeroDoctorReport(redactZeroSecrets(report) as ZeroDoctorReport));
    } catch (err: any) {
      addSystemMessage(`Doctor failed: ${redactZeroError(err).message}`);
    }
  };

  const runTuiConfigInspection = () => {
    try {
      addSystemLines(
        formatZeroConfigInspection(redactZeroSecrets(inspectZeroConfig()) as ZeroConfigInspectionReport)
      );
    } catch (err: any) {
      addSystemMessage(`Config inspection failed: ${redactZeroError(err).message}`);
    }
  };

  const runTuiSearch = async (query: string) => {
    if (!query) {
      addSystemMessage('Usage: /search <query>');
      return;
    }

    try {
      const result = await searchZeroSessions(query, { limit: 8, contextChars: 80 });
      addSystemLines(formatZeroSearchResult(result));
    } catch (err: any) {
      addSystemMessage(`Search failed: ${redactZeroError(err).message}`);
    }
  };

  const handleProviderSelected = (name: string) => {
    const success = configManager.setActiveProvider(name);
    if (success) {
      addSystemMessage(`Switched to provider: ${name}`);
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
    addSystemMessage(selectedModel
      ? `Model set for this session: ${selectedModel.displayName} (${selectedModel.provider})`
      : `Model set for this session: ${modelId}`);
    setScreen('chat');
  };

  const handleModelPickerCancel = () => {
    setScreen('chat');
  };

  const handleThemeSelected = (name: string) => {
    setTheme(name);
    configManager.setTheme(name);
    addSystemMessage(`Theme set to: ${name}`);
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

    if (!providerName) {
      addSystemMessage('Provider added successfully.');
      return;
    }

    const switched = configManager.setActiveProvider(providerName);
    if (switched) {
      setSelectedModelOverride(undefined);
    }
    addSystemMessage(switched
      ? `Added and switched to provider: ${providerName}`
      : `Provider added: ${providerName}`);
  };

  const handleAddProviderCancel = () => {
    setScreen('provider-picker');
  };

  const addMessage = (message: ChatMessage) => {
    setMessages((prev) => [...prev, message]);
  };

  const addMessages = (newMessages: ChatMessage[]) => {
    setMessages((prev) => [...prev, ...newMessages]);
  };

  const addSystemMessage = (content: string) => {
    setMessages((prev) => [...prev, { type: 'system', content }]);
  };

  const addSystemLines = (content: string) => {
    const lines = content.split(/\r?\n/).filter((line) => line.trim().length > 0);
    setMessages((prev) => [
      ...prev,
      ...lines.map((line) => ({ type: 'system' as const, content: line })),
    ]);
  };

  const requestToolApproval = async (request: ToolApprovalRequest): Promise<ToolApprovalDecision> => {
    if (approvalGrantsRef.current.has(request.grantKey)) {
      return 'allow';
    }

    setPendingApproval(request);
    return new Promise<ToolApprovalDecision>((resolve) => {
      approvalResolverRef.current = resolve;
    });
  };

  const resolvePendingApproval = (decision: ToolApprovalDecision) => {
    approvalResolverRef.current?.(decision);
    approvalResolverRef.current = null;
    setPendingApproval(null);
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

  const terminalHeight = Math.max(20, rows);
  const terminalColumns = Math.max(1, columns);
  const terminalWidth = Math.max(64, columns);
  const chatHeight = Math.max(8, terminalHeight - 6);
  const showLogo = shouldShowStartupLogo(messages);
  const maxScrollOffset = Math.max(0, messages.length - chatHeight);
  const windowEnd = Math.max(0, messages.length - scrollOffset);
  const windowStart = Math.max(0, windowEnd - chatHeight);
  const visibleMessages = messages.slice(windowStart, windowEnd);
  const hasOverflow = !showLogo && messages.length > chatHeight;
  const canScrollUp = hasOverflow && scrollOffset < maxScrollOffset;
  const canScrollDown = hasOverflow && scrollOffset > 0;
  const activeFile = deriveActiveFile(messages);
  const estimatedTokens = estimateTokens(messages);
  const contextPercent = Math.min(99, Math.round((estimatedTokens / 200000) * 100));
  const estimatedCost = Number(((estimatedTokens / 1000) * 0.003).toFixed(4));

  return (
    <TuiShell
      messages={messages}
      visibleMessages={visibleMessages}
      scrollOffset={scrollOffset}
      streamingMessageIndex={streamingMessageIndex}
      showLogo={showLogo}
      canScrollUp={canScrollUp}
      canScrollDown={canScrollDown}
      input={input}
      suggestions={suggestions}
      suggestionIndex={suggestionIndex}
      providerName={currentProviderName}
      modelName={currentModel}
      lastError={lastError}
      isPlanMode={isPlanMode}
      debugMode={debugMode}
      toolsEnabled={toolsEnabled}
      inputStyle={inputStyle}
      inputBackground={solidInputBackground}
      messageBackground={adaptedMessageBackground}
      isThinking={isThinking}
      activeFile={activeFile}
      totalTokens={estimatedTokens}
      costUsd={estimatedCost}
      contextPercent={contextPercent}
      pendingApproval={pendingApproval}
      terminalWidth={terminalWidth}
      terminalColumns={terminalColumns}
      terminalHeight={terminalHeight}
    />
  );
};

export function shouldShowStartupLogo(messages: ChatMessage[]): boolean {
  return messages.every((message) => message.type === 'system');
}

function deriveActiveFile(messages: ChatMessage[]): string | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m?.type === 'tool-call') {
      try {
        const args = JSON.parse(m.args);
        if (typeof args?.path === 'string') return args.path;
        if (typeof args?.file === 'string') return args.file;
      } catch {
        // Ignore non-JSON tool arguments.
      }
    }
  }
  return undefined;
}

function estimateTokens(messages: ChatMessage[]): number {
  const chars = messages.reduce((sum, message) => {
    const value = (message as any).content ?? (message as any).result ?? '';
    return sum + (typeof value === 'string' ? value.length : 0);
  }, 0);
  return Math.round(chars / 4);
}

function toFriendlyError(err: any): string {
  const raw = redactZeroError(err).message;
  const lower = raw.toLowerCase();

  if (lower.includes('no llm provider configured') || lower.includes('no provider')) {
    return 'No provider set up. Type /provider to add one.';
  }

  if (
    lower.includes('auth') ||
    lower.includes('unauthorized') ||
    lower.includes('invalid') ||
    lower.includes('401') ||
    lower.includes('api key')
  ) {
    return `Authentication failed - check your API key. Type /provider to update it.\n(${raw})`;
  }

  if (lower.includes('rate') || lower.includes('quota')) {
    return `Provider rate limit or quota reached. Try again shortly.\n(${raw})`;
  }

  if (
    lower.includes('enotfound') ||
    lower.includes('econnrefused') ||
    lower.includes('etimedout') ||
    lower.includes('fetch failed') ||
    lower.includes('network')
  ) {
    return `Network error reaching the provider. Check your connection and base URL.\n(${raw})`;
  }

  return `Error: ${raw}`;
}

function logDebugError(err: any): void {
  try {
    const red = '\x1b[31m';
    const reset = '\x1b[0m';
    const border = '-'.repeat(50);
    const name = err?.name || 'Error';
    const message = err?.message || String(err);

    console.error(`\n${red}+${border}+`);
    console.error(`| FULL PROVIDER ERROR${' '.repeat(30)}|`);
    console.error(`+${border}+`);
    console.error(`| Message: ${message.slice(0, 38).padEnd(38)} |`);
    console.error(`| Name:    ${name.slice(0, 38).padEnd(38)} |`);
    if (err?.response?.status) {
      console.error(`| Status:  ${String(err.response.status).padEnd(38)} |`);
    }
    console.error(`+${border}+${reset}`);
    console.error('Full object:');
    console.dir(err, { depth: 6 });
    console.error(`${red}${'='.repeat(52)}${reset}\n`);
  } catch (logErr) {
    console.error('Failed to log full error:', logErr);
  }
}
