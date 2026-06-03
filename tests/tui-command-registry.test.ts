import { describe, expect, it } from 'bun:test';
import {
  formatTuiHelpLines,
  listTuiCommandNames,
  resolveTuiCommand,
} from '../src/tui/commands';

describe('TUI command registry', () => {
  it('drives aliases, suggestions, and help output from one registry', () => {
    expect(listTuiCommandNames()).toContain('/doctor');
    expect(listTuiCommandNames()).toContain('/debug-mode');
    expect(resolveTuiCommand('/quit')?.name).toBe('/exit');

    const help = formatTuiHelpLines().join('\n');
    expect(help).toContain('/config');
    expect(help).toContain('/permissions');
    expect(help).toContain('model');
  });
});
