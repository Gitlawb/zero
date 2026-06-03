import React from 'react';
import { describe, expect, it } from 'bun:test';
import { renderToString } from 'ink';
import { TuiShell } from '../src/tui/TuiShell';
import { shouldShowStartupLogo } from '../src/tui/App';
import type { ChatMessage } from '../src/tui/types';

function renderShell(overrides: Partial<React.ComponentProps<typeof TuiShell>> = {}) {
  const messages: ChatMessage[] = [
    { type: 'system', content: 'Welcome to zero. Type /provider to manage providers.' },
    { type: 'system', content: 'Type /help for available commands.' },
  ];

  return renderToString(
    React.createElement(TuiShell, {
      messages,
      visibleMessages: messages,
      scrollOffset: 0,
      streamingMessageIndex: null,
      showLogo: true,
      canScrollUp: false,
      canScrollDown: false,
      input: '',
      suggestions: [],
      providerName: 'opengateway',
      modelName: 'gpt-5.1',
      lastError: null,
      isPlanMode: false,
      debugMode: false,
      toolsEnabled: true,
      isThinking: false,
      terminalWidth: 82,
      terminalHeight: 28,
      ...overrides,
    })
  );
}

describe('TuiShell render surface', () => {
  it('only keeps the startup wordmark visible before chat content starts', () => {
    expect(shouldShowStartupLogo([
      { type: 'system', content: 'Welcome to zero.' },
      { type: 'system', content: 'Type /help.' },
    ])).toBe(true);
    expect(shouldShowStartupLogo([
      { type: 'system', content: 'Welcome to zero.' },
      { type: 'user', content: 'inspect the repo' },
    ])).toBe(false);
    expect(shouldShowStartupLogo([
      { type: 'assistant', content: 'Done.' },
    ])).toBe(false);
  });

  it('renders the themed startup shell in the first PR style', () => {
    const output = renderShell();

    expect(output).toContain('███████╗███████╗██████╗');
    expect(output.match(/███████╗███████╗██████╗/g)?.length ?? 0).toBe(1);
    expect(output).toContain('terminal agent');
    expect(output).toContain('> █ Type your message or @path/to/file');
    expect(output).toContain('/provider');
    expect(output).toContain('Welcome to zero');
    expect(output).toContain('gpt-5.1 Model');
    expect(output).not.toContain('live');
    expect(output).not.toContain('zero >');
    expect(output).not.toContain('status: READY');
    expect(output).not.toContain('Enter');
    expect(output).not.toContain('Tab');
    expect(output).not.toContain('Ctrl+C');
    expect(output).not.toContain('Tab accepts');
    expect(output).not.toContain('shift+tab');
    expect(output).not.toContain('WORKSPACE');
    expect(output).not.toContain('SESSION');
    expect(output).not.toContain('history');
  });

  it('does not render the startup wordmark once chat mode hides it', () => {
    const output = renderShell({
      showLogo: false,
      messages: [{ type: 'user', content: 'inspect the repo' }],
      visibleMessages: [{ type: 'user', content: 'inspect the repo' }],
    });

    expect(output).not.toContain('███████╗███████╗██████╗');
    expect(output).toContain('> inspect the repo');
  });

  it('renders compact message rows and command suggestions', () => {
    const messages: ChatMessage[] = [
      { type: 'user', content: 'inspect the repo' },
      { type: 'assistant', content: 'I will scan the codebase.' },
      { type: 'tool-call', id: 'call_grep', name: 'grep', args: '{"pattern":"TODO"}', result: 'src/index.ts:1' },
      { type: 'system', content: 'Plan mode enabled.' },
    ];
    const output = renderShell({
      messages,
      visibleMessages: messages,
      showLogo: false,
      input: '/mo',
      suggestions: ['/model', '/model list'],
      isPlanMode: true,
    });

    expect(output).toContain('> inspect the repo');
    expect(output).toContain('⛬ I will scan the codebase.');
    expect(output).toContain('grep');
    expect(output).toContain('pattern: TODO');
    expect(output).toContain('[show]');
    expect(output).toContain('Plan mode enabled.');
    expect(output).toContain('model       Browse models');
    expect(output).toContain('model list');
    expect(output).toContain('▄');
    expect(output).toContain('▀');
    expect(output).toContain('ctrl+t');
    expect(output).not.toContain('perms ask');
    expect(output).not.toContain('Tab accepts');
  });

  it('renders pending tool approval with a command preview', () => {
    const messages: ChatMessage[] = [
      { type: 'user', content: 'run tests' },
      { type: 'tool-call', id: 'call_bash', name: 'bash', args: '{"command":"bun test ./tests"}' },
    ];
    const output = renderShell({
      messages,
      visibleMessages: messages,
      showLogo: false,
      isThinking: true,
      pendingApproval: {
        toolCall: { id: 'call_1', name: 'bash', arguments: '{"command":"bun test ./tests"}' },
        parsedArgs: { command: 'bun test ./tests', cwd: 'D:\\codings\\Opensource\\Zero' },
        safety: {
          sideEffect: 'shell',
          permission: 'prompt',
          reason: 'Shell commands can read, write, or execute programs.',
        },
        reason: 'Shell commands can read, write, or execute programs.',
        grantKey: 'prompt:shell',
      },
    });

    expect(output).toContain('Allow Bash?');
    expect(output).toContain('command: bun test ./tests');
    expect(output).toContain('[y]');
    expect(output).toContain('[n]');
    expect(output).toContain('[a]');
  });

  it('renders solid prompt with Music-style separators', () => {
    const messages: ChatMessage[] = [
      { type: 'system', content: 'Ready.' },
    ];
    const output = renderShell({
      messages,
      visibleMessages: messages,
      showLogo: false,
      inputStyle: 'solid',
    });

    expect(output).toContain('> █ Type your message or @path/to/file');
    expect(output).toContain('▄');
    expect(output).toContain('▀');
  });
});
