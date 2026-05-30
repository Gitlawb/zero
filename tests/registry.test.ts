import { describe, it, expect } from 'bun:test';
import { z } from 'zod';
import { ToolRegistry } from '../src/tools/registry';
import type { Tool } from '../src/tools/types';

function makeTool(name: string): Tool {
  return {
    name,
    description: `tool ${name}`,
    parameters: z.object({ x: z.string() }),
    async execute() {
      return `ran ${name}`;
    },
  };
}

describe('ToolRegistry', () => {
  it('registers and retrieves a tool by name', () => {
    const registry = new ToolRegistry();
    const tool = makeTool('alpha');
    registry.register(tool);

    expect(registry.get('alpha')).toBe(tool);
  });

  it('returns undefined for an unknown tool', () => {
    const registry = new ToolRegistry();
    expect(registry.get('missing')).toBeUndefined();
  });

  it('getAll returns every registered tool', () => {
    const registry = new ToolRegistry();
    registry.register(makeTool('a'));
    registry.register(makeTool('b'));

    const names = registry.getAll().map((t) => t.name).sort();
    expect(names).toEqual(['a', 'b']);
  });

  it('re-registering a name overwrites the previous tool', () => {
    const registry = new ToolRegistry();
    registry.register(makeTool('dup'));
    const second = makeTool('dup');
    registry.register(second);

    expect(registry.getAll()).toHaveLength(1);
    expect(registry.get('dup')).toBe(second);
  });
});
