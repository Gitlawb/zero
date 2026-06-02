import { describe, it, expect } from 'bun:test';
import { z } from 'zod';
import { ToolRegistry } from '../src/tools/registry';
import type { Tool } from '../src/tools/types';

function makeTool(name: string): Tool {
  return {
    name,
    description: `tool ${name}`,
    parameters: z.object({ x: z.string() }),
    safety: {
      sideEffect: 'read',
      permission: 'allow',
      reason: 'test tool',
    },
    async execute(args) {
      return `ran ${name}:${args.x}`;
    },
  };
}

function makePromptTool(name: string): Tool {
  return {
    ...makeTool(name),
    safety: {
      sideEffect: 'write',
      permission: 'prompt',
      reason: 'test prompt gate',
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

  it('runs tools through the validating registry path', async () => {
    const registry = new ToolRegistry();
    registry.register(makeTool('safe'));

    expect(await registry.run('safe', { x: 'ok' })).toBe('ran safe:ok');
    expect(await registry.run('safe', { x: 1 })).toContain('Invalid arguments');
  });

  it('reports unknown tools from the registry run path', async () => {
    const registry = new ToolRegistry();
    expect(await registry.run('missing', {})).toBe('Error: Unknown tool "missing".');
  });

  it('does not auto-run prompt-gated tools without a grant', async () => {
    const registry = new ToolRegistry();
    registry.register(makePromptTool('mutate'));

    const result = await registry.run('mutate', { x: 'ok' });
    expect(result).toContain('Permission required');
    expect(result).toContain('was not executed');
  });
});
