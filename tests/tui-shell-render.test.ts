import React from 'react';
import { describe, expect, it } from 'bun:test';
import { renderToString } from 'ink';
import { TuiShell } from '../src/tui/TuiShell';
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
  it('renders the startup splash without session clutter', () => {
    const output = renderShell();

    expect(output).toContain('ZERO');
    expect(output).toContain('terminal coding agent');
    expect(output).toContain('zero >');
    expect(output).toContain('/provider');
    expect(output).toContain('status: READY');
    expect(output).toContain('Enter');
    expect(output).toContain('Tab');
    expect(output).toContain('Ctrl+C');
    expect(output).not.toContain('shift+tab');
    expect(output).not.toContain('Welcome to zero');
    expect(output).not.toContain('WORKSPACE');
    expect(output).not.toContain('SESSION');
    expect(output).not.toContain('history');
  });

  it('renders compact message rows and command suggestions', () => {
    const messages: ChatMessage[] = [
      { type: 'user', content: 'inspect the repo' },
      { type: 'assistant', content: 'I will scan the codebase.' },
      { type: 'tool-call', name: 'grep', args: '{"pattern":"TODO"}', result: 'src/index.ts:1' },
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

    expect(output).toContain('>   inspect the repo');
    expect(output).toContain('◆ I will scan the codebase.');
    expect(output).toContain('Grep');
    expect(output).toContain('"TODO"');
    expect(output).toContain('• Plan mode enabled.');
    expect(output).toContain('commands /model');
  });
});
