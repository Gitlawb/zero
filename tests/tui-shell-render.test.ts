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
      ...overrides,
    })
  );
}

describe('TuiShell render surface', () => {
  it('renders a compact terminal launch surface', () => {
    const output = renderShell();

    expect(output).toContain('ZERO');
    expect(output).toContain('terminal coding agent');
    expect(output).toContain('Welcome to zero');
    expect(output).toContain('zero >');
    expect(output).toContain('/provider');
  });

  it('renders D-style message rows and command suggestions', () => {
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
    expect(output).toContain('Search pattern: TODO');
    expect(output).toContain('• Plan mode enabled.');
    expect(output).toContain('commands /model');
  });
});
