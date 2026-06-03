import React, { useRef, useState } from 'react';
import { useApp, useInput, useWindowSize } from 'ink';
import { runAgent, type ToolApprovalDecision, type ToolApprovalRequest } from '../agent/loop';
import { configManager } from '../config/manager';
import { loadProviderConfig } from '../config/provider';
import { ZERO_DEFAULT_MODEL_ID } from '../zero-model-registry';
import { redactZeroError, redactZeroSecrets, redactZeroString } from '../zero-redaction';
import { formatZeroConfigInspection, inspectZeroConfig, type ZeroConfigInspectionReport } from '../zero-config-inspection';
import { formatZeroDoctorReport, runZeroDoctor, type ZeroDoctorReport } from '../zero-doctor';
import { formatZeroSearchResult, searchZeroSessions } from '../zero-search';
import { createZeroRunContext } from '../zero-runtime';
import { AddProvider } from './AddProvider';
import { ModelPicker } from './ModelPicker';
import { ProviderPicker } from './ProviderPicker';
import { TuiShell } from './TuiShell';
import {
  formatTuiHelpLines,
  listTuiCommandNames,
} from './commands';
import {
  buildTuiModelStatus,
  formatModelListLines,
  formatModelProfileLines,
  resolveTuiModelProfileSelection,
  resolveTuiModelSelection,
} from './model-selection';
import type { ChatMessage } from './types';

type Screen = 'chat' | 'provider-picker' | 'add-provider' | 'model-picker';
const KNOWN_COMMANDS = listTuiCommandNames();
const INITIAL_MESSAGES: ChatMessage[] = [
  { type: 'system', content: 'Welcome to zero. Type /provider to manage providers.' },
  { type: 'system', content: 'Type /help for available commands.' },
];

export const App: React.FC = () => {
  const { exit } = useApp();
  const { columns, rows } = useWindowSize();
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
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [scrollOffset, setScrollOffset] = useState(0);
  const [terminalRows, setTerminalRows] = useState(24);
  const [git, setGit] = useState<{ branch?: string; ahead: number; behind: number }>({ ahead: 0, behind: 0 });
  const [pendingApproval, setPendingApproval] = useState<ToolApprovalRequest | null>(null);
  const approvalResolverRef = useRef<((decision: ToolApprovalDecision) => void) | null>(null);
  const approvalGrantsRef = useRef(new Set<string>());

  React.useEffect(() => {
    const checkProvider = async () => {
      try {
        await loadProviderConfig();
      } catch (err: any) {
        if (err.message?.includes('No LLM provider configured')) {
          addSystemMessage('No provider configured yet. Use /provider to add one.');
        }
      }
    };

    checkProvider();
  }, []);

  React.useEffect(() => {
    if (!input.startsWith('/')) {
      setSuggestions([]);
      return;
    }

    const query = input.toLowerCase();
    setSuggestions(KNOWN_COMMANDS.filter((cmd) => cmd.startsWith(query)).slice(0, 6));
  }, [input]);

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

  React.useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const { execa } = await import('execa');
        const head = await execa('git', ['rev-parse', '--abbrev-ref', 'HEAD']).catch(() => null);
        const branch = head?.stdout?.trim();
        let ahead = 0;
        let behind = 0;
        const counts = await execa('git', ['rev-list', '--left-right', '--count', '@{upstream}...HEAD']).catch(() => null);
        if (counts?.stdout) {
          const [b, a] = counts.stdout.trim().split(/\s+/).map((n) => Number(n) || 0);
          behind = b ?? 0;
          ahead = a ?? 0;
        }
        if (!cancelled && branch) setGit({ branch, ahead, behind });
      } catch {
        // Header git status is best effort.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  React.useEffect(() => {
    if (scrollOffset <= 3) {
      setScrollOffset(0);
    }
  }, [messages.length]);

  const activeProfile = configManager.getActiveProvider();
  const modelStatus = buildTuiModelStatus(
    activeProfile
      ? {
          model: activeProfile.model,
          provider: activeProfile.provider,
          profileName: activeProfile.name,
          source: 'profile',
        }
      : {
          model: process.env.OPENAI_MODEL || ZERO_DEFAULT_MODEL_ID,
          source: process.env.ZERO_PROVIDER_COMMAND ? 'provider-command' : 'environment',
        },
    selectedModelOverride
  );
  const currentProviderName = activeProfile?.name || modelStatus.providerLabel;
  const currentModel = `${modelStatus.label}${modelStatus.sourceLabel === 'session' ? ' *' : ''}`;
  const isInChat = screen === 'chat';

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

    if (!input) {
      if (key.upArrow) {
        setScrollOffset((prev) => Math.min(prev + 1, messages.length - 1));
        return;
      }
      if (key.downArrow) {
        setScrollOffset((prev) => Math.max(prev - 1, 0));
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
    }

    if (key.return) {
      handleSubmit();
      return;
    }

    if (key.tab && suggestions.length > 0) {
      setInput(`${suggestions[0]} `);
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
          addMessage({ type: 'tool-call', name: tc.name, args: redactZeroString(tc.arguments) });
        },
        onToolResult: (result) => {
          setMessages((prev) => {
            const next = [...prev];
            for (let i = next.length - 1; i >= 0; i--) {
              const msg = next[i];
              if (msg?.type === 'tool-call' && msg.result === undefined) {
                next[i] = { ...msg, result: redactZeroString(result.result) };
                break;
              }
            }
            return next;
          });
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
        activeModelId={modelStatus.knownModel?.id || modelStatus.modelId}
        onSelect={handleModelSelected}
        onCancel={handleModelPickerCancel}
      />
    );
  }

  const terminalHeight = Math.max(20, rows || terminalRows);
  const terminalWidth = Math.max(64, columns || process.stdout.columns || 96);
  const showLogo = messages.every((message) => message.type === 'system');
  const chatHeight = Math.max(7, terminalHeight - 14);
  const visibleMessages = showLogo
    ? messages
    : messages.slice(scrollOffset, scrollOffset + chatHeight);
  const canScrollUp = scrollOffset < messages.length - 1;
  const canScrollDown = scrollOffset > 0;
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
      providerName={currentProviderName}
      modelName={currentModel}
      lastError={lastError}
      isPlanMode={isPlanMode}
      debugMode={debugMode}
      toolsEnabled={toolsEnabled}
      isThinking={isThinking}
      activeFile={activeFile}
      branch={git.branch}
      ahead={git.ahead}
      behind={git.behind}
      totalTokens={estimatedTokens}
      costUsd={estimatedCost}
      contextPercent={contextPercent}
      pendingApproval={pendingApproval}
      terminalWidth={terminalWidth}
      terminalHeight={terminalHeight}
    />
  );
};

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
