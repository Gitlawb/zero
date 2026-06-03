import React from 'react';
import { render } from 'ink';
import { App } from './App';
import { getHighlighter } from './highlighter';
import { detectTerminalBackground } from './terminal-background';

// Preload Shiki highlighter early for fast syntax highlighting
getHighlighter().catch(() => {
  // Silently fail - we'll fallback gracefully
});

export async function startTUI() {
  const terminalBackground = await detectTerminalBackground().catch(() => undefined);
  render(<App initialTerminalBackground={terminalBackground} />);
}
