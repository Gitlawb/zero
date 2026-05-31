/** @jsxImportSource @opentui/solid */
import { TextAttributes, type KeyEvent } from '@opentui/core';
import { useKeyboard, usePaste, useTerminalDimensions } from '@opentui/solid';
import { For, Match, Show, Switch, createMemo, createSignal, onMount } from 'solid-js';
import { configManager } from '../config/manager';
import { loadProviderConfig } from '../config/provider';
import type { ProviderProfile } from '../config/types';
import { runAgent } from '../agent/loop';
import { OpenAIProvider } from '../providers/openai';
import type { PlanItem } from '../tools/plan';

type Screen = 'chat' | 'providers' | 'add-provider';

type ChatMessage =
  | { type: 'user'; content: string }
  | { type: 'assistant'; content: string; streaming?: boolean }
  | { type: 'tool-call'; name: string; args: string; result?: string }
  | { type: 'system'; content: string };

type Usage = {
  promptTokens: number;
  completionTokens: number;
};

type AddProviderDraft = {
  name: string;
  baseURL: string;
  apiKey: string;
  model: string;
};

type SlashCommand = {
  name: string;
  detail: string;
  run: () => void;
};

function toFriendlyError(error: unknown): string {
  const raw = error instanceof Error ? error.message : String(error);
  const lower = raw.toLowerCase();

  if (lower.includes('no llm provider configured') || lower.includes('no provider')) {
    return 'No provider set up. Type /provider to add one.';
  }

  if (lower.includes('auth') || lower.includes('unauthorized') || lower.includes('invalid') || lower.includes('401') || lower.includes('api key')) {
    return `Authentication failed - check your API key. Type /provider to update it.\n(${raw})`;
  }

  if (lower.includes('rate') || lower.includes('quota')) {
    return `Provider rate limit or quota reached. Try again shortly.\n(${raw})`;
  }

  if (lower.includes('enotfound') || lower.includes('econnrefused') || lower.includes('etimedout') || lower.includes('fetch failed') || lower.includes('network')) {
    return `Network error reaching the provider. Check your connection and base URL.\n(${raw})`;
  }

  return `Error: ${raw}`;
}

const colors = {
  bg: '#080b0d',
  surface: '#101417',
  line: '#1d252a',
  text: '#d8dee4',
  muted: '#7d8790',
  subtle: '#53606a',
  accent: '#61d394',
  cyan: '#66d9ef',
  blue: '#7aa2f7',
  magenta: '#c792ea',
  yellow: '#e6c76e',
  error: '#ff6b6b',
  success: '#80d996',
};

function printable(event: KeyEvent): string | undefined {
  if (event.ctrl || event.meta || event.super) return undefined;
  if (event.name === 'space' || event.name === ' ') return ' ';
  if (event.name.length === 1) return event.name;
  return undefined;
}

function normalizePastedText(input: string, singleLine = false) {
  const normalized = input.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
  return singleLine ? normalized.replace(/\n/g, ' ') : normalized;
}

async function readClipboardText(): Promise<string | undefined> {
  if (process.platform === 'win32') {
    const proc = Bun.spawn(['powershell', '-NoProfile', '-Command', 'Get-Clipboard -Raw'], {
      stdout: 'pipe',
      stderr: 'ignore',
    });
    const output = await new Response(proc.stdout).text();
    const code = await proc.exited;
    return code === 0 ? output : undefined;
  }

  return undefined;
}

function trimLine(input: string, max = 120) {
  const line = input.replace(/\s+/g, ' ').trim();
  return line.length > max ? `${line.slice(0, max - 1)}...` : line;
}

function statusText(status: PlanItem['status']) {
  switch (status) {
    case 'completed':
      return '[x]';
    case 'in_progress':
      return '[>]';
    case 'failed':
      return '[!]';
    default:
      return '[ ]';
  }
}

function formatJson(input: string) {
  try {
    return JSON.stringify(JSON.parse(input || '{}'), null, 2);
  } catch {
    return input;
  }
}

function commandHelp() {
  return [
    { type: 'system' as const, content: 'Available commands:' },
    { type: 'system' as const, content: '  /provider  Manage local provider profiles' },
    { type: 'system' as const, content: '  /plan      Toggle plan mode styling' },
    { type: 'system' as const, content: '  /todo      Show or hide the todo rail' },
    { type: 'system' as const, content: '  /tools     Toggle tool calling' },
    { type: 'system' as const, content: '  /debug     Show the last provider error' },
    { type: 'system' as const, content: '  /debug-mode Toggle provider payload logging' },
    { type: 'system' as const, content: '  /help      Show this help' },
    { type: 'system' as const, content: '  /exit      Quit' },
  ];
}

export function App(props: { onExit: () => void }) {
  const term = useTerminalDimensions();
  const [screen, setScreen] = createSignal<Screen>('chat');
  const [messages, setMessages] = createSignal<ChatMessage[]>([
    { type: 'system', content: 'Welcome to zero. Type /provider to manage providers.' },
    { type: 'system', content: 'Type /help for available commands.' },
  ]);
  const [input, setInput] = createSignal('');
  const [isThinking, setIsThinking] = createSignal(false);
  const [isPlanMode, setIsPlanMode] = createSignal(false);
  const [debugMode, setDebugMode] = createSignal(false);
  const [lastError, setLastError] = createSignal<unknown>(null);
  const [toolsEnabled, setToolsEnabled] = createSignal(true);
  const [scrollOffset, setScrollOffset] = createSignal(0);
  const [usage, setUsage] = createSignal<Usage>({ promptTokens: 0, completionTokens: 0 });
  const [plan, setPlan] = createSignal<PlanItem[]>([]);
  const [todoRailOpen, setTodoRailOpen] = createSignal(false);
  const [todoRailHidden, setTodoRailHidden] = createSignal(false);
  const [commandCursor, setCommandCursor] = createSignal(0);
  const [providerCursor, setProviderCursor] = createSignal(0);
  const [providerVersion, setProviderVersion] = createSignal(0);
  const [draft, setDraft] = createSignal<AddProviderDraft>({
    name: '',
    baseURL: 'https://api.openai.com/v1',
    apiKey: '',
    model: 'gpt-4o',
  });
  const [draftField, setDraftField] = createSignal<keyof AddProviderDraft>('name');

  const providers = createMemo(() => {
    providerVersion();
    return configManager.listProviders();
  });
  const activeProvider = createMemo(() => {
    providerVersion();
    return configManager.getActiveProvider();
  });
  const providerName = createMemo(() => activeProvider()?.name || (process.env.ZERO_PROVIDER_COMMAND ? 'command' : 'env'));
  const modelName = createMemo(() => activeProvider()?.model || process.env.OPENAI_MODEL || 'default');
  const totalTokens = createMemo(() => usage().promptTokens + usage().completionTokens);
  const hasPlanItems = createMemo(() => plan().length > 0);
  const hasActivePlanItems = createMemo(() => plan().some((item) => item.status !== 'completed'));
  const rightRail = createMemo(() =>
    term().width >= 86 && (todoRailOpen() || (hasPlanItems() && hasActivePlanItems() && !todoRailHidden())),
  );
  const transcriptHeight = createMemo(() => Math.max(6, term().height - 7));
  const visibleMessages = createMemo(() => messages().slice(scrollOffset(), scrollOffset() + transcriptHeight()));
  const canScrollUp = createMemo(() => scrollOffset() < Math.max(0, messages().length - 1));
  const canScrollDown = createMemo(() => scrollOffset() > 0);
  const providerOptions = createMemo(() => [...providers(), { name: '+ add provider' } as Pick<ProviderProfile, 'name'>]);
  const slashCommands = createMemo<SlashCommand[]>(() => [
    {
      name: '/provider',
      detail: 'Manage local provider profiles',
      run: () => {
        setProviderCursor(0);
        setScreen('providers');
      },
    },
    {
      name: '/plan',
      detail: 'Toggle plan mode styling',
      run: () => {
        setIsPlanMode((value) => !value);
        append({ type: 'system', content: `Plan mode ${isPlanMode() ? 'enabled' : 'disabled'}.` });
      },
    },
    {
      name: '/todo',
      detail: 'Show or hide the todo rail',
      run: () => toggleTodoRail(),
    },
    {
      name: '/tools',
      detail: 'Toggle model tool calling',
      run: () => {
        setToolsEnabled((value) => !value);
        append({ type: 'system', content: `Tools ${toolsEnabled() ? 'enabled' : 'disabled'}.` });
      },
    },
    {
      name: '/debug',
      detail: 'Show the last provider error',
      run: () => {
        const error = lastError();
        append({ type: 'system', content: error ? toFriendlyError(error) : 'No provider error recorded yet.' });
      },
    },
    {
      name: '/debug-mode',
      detail: 'Toggle provider payload logging',
      run: () => {
        setDebugMode((value) => !value);
        append({ type: 'system', content: `Debug mode ${debugMode() ? 'enabled' : 'disabled'}.` });
      },
    },
    {
      name: '/help',
      detail: 'Show available commands',
      run: () => setMessages((prev) => [...prev, ...commandHelp()]),
    },
    {
      name: '/exit',
      detail: 'Quit zero',
      run: props.onExit,
    },
  ]);
  const filteredCommands = createMemo(() => {
    const query = input().slice(1).toLowerCase();
    const matches = slashCommands().filter((command) => command.name.slice(1).startsWith(query));
    return matches;
  });
  const commandMenuOpen = createMemo(() => screen() === 'chat' && input().startsWith('/'));

  onMount(() => {
    void loadProviderConfig().catch((err: Error) => {
      if (err.message.includes('No LLM provider configured')) {
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: 'No provider configured yet. Use /provider to add one.' },
        ]);
      }
    });
  });

  function append(message: ChatMessage) {
    setMessages((prev) => [...prev, message]);
    if (scrollOffset() <= 3) setScrollOffset(0);
  }

  function updateLastAssistant(text: string) {
    setMessages((prev) => {
      const next = [...prev];
      const last = next[next.length - 1];
      if (last?.type === 'assistant') {
        next[next.length - 1] = { ...last, content: last.content + text, streaming: true };
        return next;
      }
      return [...next, { type: 'assistant', content: text, streaming: true }];
    });
  }

  function stopAssistantStreaming() {
    setMessages((prev) =>
      prev.map((item) => (item.type === 'assistant' ? { ...item, streaming: false } : item)),
    );
  }

  function attachToolResult(result: string) {
    setMessages((prev) => {
      const next = [...prev];
      for (let i = next.length - 1; i >= 0; i--) {
        const item = next[i];
        if (item?.type === 'tool-call' && item.result === undefined) {
          next[i] = { ...item, result };
          break;
        }
      }
      return next;
    });
  }

  function updatePlan(nextPlan: PlanItem[]) {
    if (nextPlan.length > 0 && plan().length === 0) {
      setTodoRailHidden(false);
      setTodoRailOpen(false);
    }
    setPlan(nextPlan);
  }

  function toggleTodoRail() {
    if (rightRail()) {
      setTodoRailOpen(false);
      setTodoRailHidden(true);
    } else {
      setTodoRailHidden(false);
      setTodoRailOpen(true);
    }
  }

  function submitChat() {
    const trimmed = input().trim();
    if (!trimmed || isThinking()) return;
    setInput('');
    append({ type: 'user', content: trimmed });

    if (trimmed.startsWith('/')) {
      handleSlash(trimmed);
      return;
    }

    void runPrompt(trimmed);
  }

  async function runPrompt(prompt: string) {
    setIsThinking(true);
    append({ type: 'assistant', content: '', streaming: true });

    try {
      const providerConfig = await loadProviderConfig();
      const provider = new OpenAIProvider({
        apiKey: providerConfig.apiKey || '',
        baseURL: providerConfig.baseURL,
        model: providerConfig.model,
      });

      await runAgent(prompt, provider, {
        debug: debugMode(),
        toolsEnabled: toolsEnabled(),
        planMode: isPlanMode(),
        onText: (text) => {
          setIsThinking(false);
          updateLastAssistant(text);
        },
        onToolCall: (toolCall) => {
          setIsThinking(false);
          stopAssistantStreaming();
          append({ type: 'tool-call', name: toolCall.name, args: toolCall.arguments });
        },
        onToolResult: (result) => attachToolResult(result.result),
        onUsage: (next) => {
          setUsage((current) => ({
            promptTokens: current.promptTokens + next.promptTokens,
            completionTokens: current.completionTokens + next.completionTokens,
          }));
        },
        onPlanUpdate: (nextPlan) => updatePlan(nextPlan),
      });
    } catch (err) {
      setLastError(err);
      append({ type: 'system', content: toFriendlyError(err) });
    } finally {
      setIsThinking(false);
      stopAssistantStreaming();
    }
  }

  function handleSlash(command: string) {
    const cmd = command.toLowerCase();
    const matched = slashCommands().find((item) => item.name === cmd || (cmd === '/quit' && item.name === '/exit') || (cmd === '/debug-mode false' && item.name === '/debug-mode'));
    if (matched) {
      matched.run();
      return;
    }
    append({ type: 'system', content: `Unknown command: ${command}` });
  }

  function acceptSlashCommand() {
    const selected = filteredCommands()[commandCursor()];
    if (!selected) {
      submitChat();
      return true;
    }
    setInput('');
    append({ type: 'user', content: selected.name });
    selected.run();
    return true;
  }

  function saveProvider() {
    const value = draft();
    if (!value.name.trim() || !value.baseURL.trim() || !value.model.trim()) {
      append({ type: 'system', content: 'Provider name, base URL, and model are required.' });
      return;
    }

    configManager.addProvider({
      name: value.name.trim(),
      baseURL: value.baseURL.trim(),
      apiKey: value.apiKey.trim(),
      model: value.model.trim(),
    });
    configManager.setActiveProvider(value.name.trim());
    setProviderVersion((version) => version + 1);
    setDraft({ name: '', baseURL: 'https://api.openai.com/v1', apiKey: '', model: 'gpt-4o' });
    setDraftField('name');
    setScreen('chat');
    append({ type: 'system', content: `Added and switched to provider: ${value.name.trim()}` });
  }

  function editDraft(event: KeyEvent) {
    const field = draftField();
    if (event.name === 'return') {
      const order: (keyof AddProviderDraft)[] = ['name', 'baseURL', 'apiKey', 'model'];
      const index = order.indexOf(field);
      if (index === order.length - 1) saveProvider();
      else setDraftField(order[index + 1]!);
      event.preventDefault();
      return;
    }
    if (event.name === 'tab') {
      const order: (keyof AddProviderDraft)[] = ['name', 'baseURL', 'apiKey', 'model'];
      const index = order.indexOf(field);
      setDraftField(order[(index + (event.shift ? -1 : 1) + order.length) % order.length]!);
      event.preventDefault();
      return;
    }
    if (event.name === 'escape') {
      setScreen('providers');
      event.preventDefault();
      return;
    }
    if (event.name === 'backspace' || event.name === 'delete') {
      setDraft((current) => ({ ...current, [field]: current[field].slice(0, -1) }));
      event.preventDefault();
      return;
    }
    const char = printable(event);
    if (char) {
      setDraft((current) => ({ ...current, [field]: current[field] + char }));
      event.preventDefault();
    }
  }

  function insertText(text: string) {
    if (!text) return;

    if (screen() === 'add-provider') {
      const field = draftField();
      setDraft((current) => ({
        ...current,
        [field]: current[field] + normalizePastedText(text, true).trimEnd(),
      }));
      return;
    }

    if (screen() === 'chat') {
      setInput((value) => value + normalizePastedText(text));
    }
  }

  usePaste((event) => {
    if (event.defaultPrevented) return;
    insertText(new TextDecoder().decode(event.bytes));
    event.preventDefault();
  });

  useKeyboard((event) => {
    if (event.defaultPrevented) return;
    if (event.ctrl && event.name === 'c') {
      if (screen() === 'add-provider') {
        setScreen('providers');
        event.preventDefault();
        return;
      }
      if (screen() === 'providers') {
        setScreen('chat');
        event.preventDefault();
        return;
      }
      if (input()) {
        setInput('');
        setCommandCursor(0);
        event.preventDefault();
        return;
      }
      props.onExit();
      event.preventDefault();
      return;
    }
    if ((event.ctrl && event.name === 'v') || event.raw === '\x16') {
      void readClipboardText().then((text) => {
        if (text) insertText(text);
      });
      event.preventDefault();
      return;
    }
    if ((event.ctrl && event.name === 't') || event.raw === '\x14') {
      toggleTodoRail();
      event.preventDefault();
      return;
    }

    if (screen() === 'add-provider') {
      editDraft(event);
      return;
    }

    if (screen() === 'providers') {
      if (event.name === 'escape') {
        setScreen('chat');
        event.preventDefault();
        return;
      }
      if (event.name === 'up' || event.name === 'k') {
        setProviderCursor((value) => Math.max(0, value - 1));
        event.preventDefault();
        return;
      }
      if (event.name === 'down' || event.name === 'j') {
        setProviderCursor((value) => Math.min(providerOptions().length - 1, value + 1));
        event.preventDefault();
        return;
      }
      if (event.name === 'return') {
        const selected = providerOptions()[providerCursor()];
        if (!selected) return;
        if (selected.name === '+ add provider') {
          setScreen('add-provider');
        } else if (configManager.setActiveProvider(selected.name)) {
          setProviderVersion((version) => version + 1);
          setScreen('chat');
          append({ type: 'system', content: `Switched to provider: ${selected.name}` });
        }
        event.preventDefault();
        return;
      }
      return;
    }

    if (commandMenuOpen() && (event.name === 'up' || event.name === 'down')) {
      setCommandCursor((value) => {
        const count = filteredCommands().length;
        if (count === 0) return 0;
        const next = event.name === 'up' ? value - 1 : value + 1;
        return (next + count) % count;
      });
      event.preventDefault();
      return;
    }
    if (commandMenuOpen() && event.name === 'tab') {
      const selected = filteredCommands()[commandCursor()];
      if (selected) setInput(selected.name);
      event.preventDefault();
      return;
    }
    if (commandMenuOpen() && event.name === 'escape') {
      setInput('');
      setCommandCursor(0);
      event.preventDefault();
      return;
    }
    if (event.name === 'return') {
      if (commandMenuOpen() && acceptSlashCommand()) {
        event.preventDefault();
        return;
      }
      submitChat();
      event.preventDefault();
      return;
    }
    if (event.name === 'backspace' || event.name === 'delete') {
      setInput((value) => value.slice(0, -1));
      event.preventDefault();
      return;
    }
    if (!input()) {
      if (event.name === 'up') {
        setScrollOffset((value) => Math.min(value + 1, Math.max(0, messages().length - 1)));
        event.preventDefault();
        return;
      }
      if (event.name === 'down') {
        setScrollOffset((value) => Math.max(0, value - 1));
        event.preventDefault();
        return;
      }
      if (event.name === 'pageup') {
        setScrollOffset((value) => Math.min(value + 8, Math.max(0, messages().length - 1)));
        event.preventDefault();
        return;
      }
      if (event.name === 'pagedown') {
        setScrollOffset((value) => Math.max(0, value - 8));
        event.preventDefault();
        return;
      }
    }

    const char = printable(event);
    if (char) {
      setInput((value) => {
        const next = value + char;
        if (next.startsWith('/')) setCommandCursor(0);
        return next;
      });
      event.preventDefault();
    }
  });

  return (
    <box width="100%" height="100%" flexDirection="column" backgroundColor={colors.bg}>
      <Header
        provider={providerName()}
        model={modelName()}
        usage={usage()}
        total={totalTokens()}
        mode={isPlanMode() ? 'plan' : 'build'}
      />

      <box width="100%" flexGrow={1} minHeight={0} flexDirection="row">
        <box width={rightRail() ? '68%' : '100%'} flexGrow={1} flexDirection="column" paddingLeft={1} paddingRight={1}>
          <Switch>
            <Match when={screen() === 'chat'}>
              <Transcript
                messages={visibleMessages()}
                canScrollUp={canScrollUp()}
                canScrollDown={canScrollDown()}
                thinking={isThinking()}
              />
            </Match>
            <Match when={screen() === 'providers'}>
              <ProviderView providers={providerOptions()} cursor={providerCursor()} active={activeProvider()?.name} />
            </Match>
            <Match when={screen() === 'add-provider'}>
              <AddProviderView draft={draft()} field={draftField()} />
            </Match>
          </Switch>
        </box>

        <Show when={rightRail()}>
          <box width={1} height="100%" backgroundColor={colors.line} />
          <TodoRail plan={plan()} />
        </Show>
      </box>

      <Show when={debugMode() && lastError()}>
        <DebugError error={lastError()} />
      </Show>

      <Show when={commandMenuOpen()}>
        <CommandMenu commands={filteredCommands()} cursor={commandCursor()} />
      </Show>

      <Composer
        screen={screen()}
        input={input()}
        planMode={isPlanMode()}
        provider={providerName()}
        model={modelName()}
        todoVisible={rightRail()}
        hasTodos={hasPlanItems()}
        debugMode={debugMode()}
        toolsEnabled={toolsEnabled()}
      />
    </box>
  );
}

function Header(props: { provider: string; model: string; usage: Usage; total: number; mode: string }) {
  return (
    <box width="100%" height={3} flexDirection="column" backgroundColor={colors.surface} flexShrink={0}>
      <box width="100%" flexDirection="row" justifyContent="space-between" paddingLeft={1} paddingRight={1}>
        <text fg={colors.text} attributes={TextAttributes.BOLD}>
          zero
          <span style={{ fg: colors.subtle }}> / local agent</span>
        </text>
        <text fg={colors.muted} wrapMode="none" truncate>
          {props.provider} <span style={{ fg: colors.subtle }}>/</span> <span style={{ fg: colors.magenta }}>{props.model}</span>
        </text>
      </box>
      <box width="100%" flexDirection="row" justifyContent="space-between" paddingLeft={1} paddingRight={1}>
        <text fg={props.mode === 'plan' ? colors.accent : colors.muted}>mode {props.mode}</text>
        <text fg={colors.subtle} wrapMode="none">
          usage p:{props.usage.promptTokens} c:{props.usage.completionTokens} t:{props.total}
        </text>
      </box>
    </box>
  );
}

function Transcript(props: {
  messages: ChatMessage[];
  canScrollUp: boolean;
  canScrollDown: boolean;
  thinking: boolean;
}) {
  return (
    <box width="100%" height="100%" flexDirection="column" paddingTop={1}>
      <Show when={props.canScrollUp || props.canScrollDown}>
        <text fg={colors.subtle} wrapMode="none">
          {props.canScrollUp ? 'up available' : 'top'} / {props.canScrollDown ? 'down available' : 'bottom'}
        </text>
      </Show>
      <For each={props.messages}>
        {(message) => (
          <box width="100%" flexDirection="column" marginBottom={1}>
            <Message message={message} />
          </box>
        )}
      </For>
      <Show when={props.thinking}>
        <text fg={colors.muted}>thinking...</text>
      </Show>
    </box>
  );
}

function Message(props: { message: ChatMessage }) {
  return (
    <Switch>
      <Match when={props.message.type === 'user'}>
        <text fg={colors.blue} wrapMode="word">{`> ${(props.message as any).content}`}</text>
      </Match>
      <Match when={props.message.type === 'assistant'}>
        <box flexDirection="row" width="100%">
          <text fg={colors.cyan} flexShrink={0}>| </text>
          <text fg={colors.text} wrapMode="word">
            {(props.message as any).content}
            <Show when={(props.message as any).streaming}>
              <span style={{ fg: colors.cyan }}> _</span>
            </Show>
          </text>
        </box>
      </Match>
      <Match when={props.message.type === 'tool-call'}>
        <ToolCall message={props.message as Extract<ChatMessage, { type: 'tool-call' }>} />
      </Match>
      <Match when={props.message.type === 'system'}>
        <text fg={colors.muted} wrapMode="word">{(props.message as any).content}</text>
      </Match>
    </Switch>
  );
}

function ToolCall(props: { message: Extract<ChatMessage, { type: 'tool-call' }> }) {
  return (
    <box width="100%" flexDirection="column" paddingLeft={1}>
      <text fg={props.message.result ? colors.success : colors.yellow} wrapMode="none">
        tool {props.message.name} {props.message.result ? 'done' : 'running'}
      </text>
      <text fg={colors.subtle} wrapMode="word">{trimLine(formatJson(props.message.args), 180)}</text>
      <Show when={props.message.result}>
        <text fg={colors.muted} wrapMode="word">{trimLine(props.message.result || '', 180)}</text>
      </Show>
    </box>
  );
}

function TodoRail(props: { plan: PlanItem[] }) {
  return (
    <box
      width="32%"
      height="100%"
      flexDirection="column"
      paddingLeft={1}
      paddingRight={1}
      paddingTop={1}
    >
      <text fg={colors.text} attributes={TextAttributes.BOLD}>todo</text>
      <text fg={colors.subtle} wrapMode="word">updates from update_plan appear here</text>
      <box height={1} />
      <Show
        when={props.plan.length > 0}
        fallback={<text fg={colors.muted} wrapMode="word">No active todo list yet.</text>}
      >
        <For each={props.plan}>
          {(item, index) => (
            <box flexDirection="column" marginBottom={1}>
              <text
                fg={item.status === 'in_progress' ? colors.accent : item.status === 'completed' ? colors.success : colors.muted}
                wrapMode="word"
              >
                {index() + 1}. {statusText(item.status)} {item.content}
              </text>
              <Show when={item.notes}>
                <text fg={colors.subtle} wrapMode="word">   {item.notes}</text>
              </Show>
            </box>
          )}
        </For>
      </Show>
    </box>
  );
}

function CommandMenu(props: { commands: SlashCommand[]; cursor: number }) {
  return (
    <box width="100%" flexDirection="column" paddingLeft={1} paddingRight={1} flexShrink={0}>
      <box width="100%" flexDirection="column" backgroundColor={colors.surface} paddingLeft={1} paddingRight={1}>
        <text fg={colors.subtle}>commands</text>
        <For each={props.commands.slice(0, 5)}>
          {(command, index) => {
            const selected = createMemo(() => index() === props.cursor);
            return (
              <box width="100%" flexDirection="row" justifyContent="space-between">
                <text fg={selected() ? colors.accent : colors.text}>
                  {selected() ? '> ' : '  '}{command.name}
                </text>
                <text fg={selected() ? colors.text : colors.subtle} wrapMode="none" truncate>
                  {command.detail}
                </text>
              </box>
            );
          }}
        </For>
      </box>
    </box>
  );
}

function DebugError(props: { error: unknown }) {
  const message = () => props.error instanceof Error ? props.error.message : String(props.error);

  return (
    <box width="100%" flexDirection="column" paddingLeft={1} paddingRight={1} flexShrink={0}>
      <box width="100%" flexDirection="column" backgroundColor={colors.surface} paddingLeft={1} paddingRight={1}>
        <text fg={colors.error} attributes={TextAttributes.BOLD}>debug error</text>
        <text fg={colors.muted} wrapMode="word">{trimLine(message(), 240)}</text>
      </box>
    </box>
  );
}

function ProviderView(props: {
  providers: Pick<ProviderProfile, 'name'>[];
  cursor: number;
  active?: string;
}) {
  return (
    <box width="100%" height="100%" flexDirection="column" paddingTop={1}>
      <text fg={colors.text} attributes={TextAttributes.BOLD}>providers</text>
      <text fg={colors.subtle}>enter select / esc back</text>
      <box height={1} />
      <For each={props.providers}>
        {(provider, index) => {
          const selected = createMemo(() => index() === props.cursor);
          return (
            <box width="100%" flexDirection="row">
              <text fg={selected() ? colors.accent : colors.subtle}>{selected() ? '> ' : '  '}</text>
              <text fg={selected() ? colors.text : colors.muted}>
                {provider.name}
                <Show when={props.active === provider.name}>
                  <span style={{ fg: colors.accent }}> active</span>
                </Show>
              </text>
            </box>
          );
        }}
      </For>
    </box>
  );
}

function AddProviderView(props: { draft: AddProviderDraft; field: keyof AddProviderDraft }) {
  const rows: { key: keyof AddProviderDraft; label: string; secret?: boolean }[] = [
    { key: 'name', label: 'name' },
    { key: 'baseURL', label: 'base url' },
    { key: 'apiKey', label: 'api key', secret: true },
    { key: 'model', label: 'model' },
  ];

  return (
    <box width="100%" height="100%" flexDirection="column" paddingTop={1}>
      <text fg={colors.text} attributes={TextAttributes.BOLD}>add provider</text>
      <text fg={colors.subtle}>tab move / enter next-save / esc back</text>
      <box height={1} />
      <For each={rows}>
        {(row) => {
          const active = createMemo(() => props.field === row.key);
          const value = createMemo(() => row.secret ? '*'.repeat(props.draft[row.key].length) : props.draft[row.key]);
          return (
            <box flexDirection="row" width="100%" marginBottom={1}>
              <text fg={active() ? colors.accent : colors.subtle} flexShrink={0}>
                {active() ? '> ' : '  '}{row.label}:{' '}
              </text>
              <text fg={colors.text} wrapMode="none" truncate>
                {value()}
                <Show when={active()}>
                  <span style={{ fg: colors.cyan }}>_</span>
                </Show>
              </text>
            </box>
          );
        }}
      </For>
    </box>
  );
}

function Composer(props: {
  screen: Screen;
  input: string;
  planMode: boolean;
  provider: string;
  model: string;
  todoVisible: boolean;
  hasTodos: boolean;
  debugMode: boolean;
  toolsEnabled: boolean;
}) {
  const inChat = () => props.screen === 'chat';
  return (
    <box width="100%" flexDirection="column" flexShrink={0} backgroundColor={colors.bg}>
      <box width="100%" height={1} backgroundColor={props.planMode ? colors.accent : colors.line} />
      <box
        width="100%"
        minHeight={3}
        paddingLeft={1}
        paddingRight={1}
        flexDirection="column"
      >
        <Show
          when={inChat()}
          fallback={<text fg={colors.muted}>provider screen active</text>}
        >
          <box width="100%" flexDirection="row" justifyContent="space-between">
            <text fg={props.planMode ? colors.accent : colors.text} wrapMode="word">
              <span style={{ fg: props.planMode ? colors.accent : colors.cyan }}>&gt;</span> {props.input}
              <span style={{ fg: colors.subtle }}>_</span>
            </text>
            <text fg={colors.subtle} wrapMode="none" truncate>
              {props.provider} / {props.model}
            </text>
          </box>
        </Show>
      </box>
      <box width="100%" paddingLeft={1}>
        <text fg={colors.subtle}>/ commands - ctrl+t todo - arrows scroll - ctrl+c exit</text>
        <Show when={props.planMode}>
          <text fg={colors.accent}> - plan mode</text>
        </Show>
        <Show when={!props.toolsEnabled}>
          <text fg={colors.yellow}> - tools off</text>
        </Show>
        <Show when={props.debugMode}>
          <text fg={colors.error}> - debug</text>
        </Show>
        <Show when={props.hasTodos}>
          <text fg={props.todoVisible ? colors.accent : colors.subtle}>
            {' '}- todo {props.todoVisible ? 'shown' : 'hidden'}
          </text>
        </Show>
      </box>
    </box>
  );
}
