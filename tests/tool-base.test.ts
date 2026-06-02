import { describe, it, expect } from 'bun:test';
import { z } from 'zod';
import { ToolBase } from '../src/tools/base';

class EchoTool extends ToolBase {
  readonly name = 'echo';
  readonly description = 'Returns its input back.';
  readonly parameters = z.object({ message: z.string() });
  readonly safety = {
    sideEffect: 'read' as const,
    permission: 'allow' as const,
    reason: 'test tool',
  };

  async execute(args: z.infer<typeof this.parameters>) {
    return `echo: ${args.message}`;
  }
}

class FailingTool extends ToolBase {
  readonly name = 'boom';
  readonly description = 'Always throws.';
  readonly parameters = z.object({});
  readonly safety = {
    sideEffect: 'read' as const,
    permission: 'allow' as const,
    reason: 'test tool',
  };

  async execute(_args: Record<string, unknown>): Promise<string> {
    throw new Error('kaboom');
  }
}

describe('ToolBase', () => {
  it('exposes name, description, and parameters', () => {
    const tool = new EchoTool();
    expect(tool.name).toBe('echo');
    expect(tool.description).toBe('Returns its input back.');
    expect(tool.parameters).toBeInstanceOf(z.ZodObject);
  });

  it('runs parsed arguments through execute()', async () => {
    const tool = new EchoTool();
    const result = await tool.execute({ message: 'hi' });
    expect(result).toBe('echo: hi');
  });

  it('produces a valid object JSON Schema', () => {
    const tool = new EchoTool();
    const schema = tool.toJSONSchema();
    expect(schema.type).toBe('object');
    expect((schema as any).properties.message.type).toBe('string');
    expect((schema as any).required).toContain('message');
    expect((schema as any).additionalProperties).toBe(false);
  });
});
