import { describe, it, expect } from 'bun:test';
import { z } from 'zod';
import { ToolBase } from '../src/tools/base';

class EchoTool extends ToolBase {
  readonly name = 'echo';
  readonly description = 'Returns its input back.';
  readonly parameters = z.object({ message: z.string() });

  async execute(args: z.infer<typeof this.parameters>) {
    return `echo: ${args.message}`;
  }
}

class FailingTool extends ToolBase {
  readonly name = 'boom';
  readonly description = 'Always throws.';
  readonly parameters = z.object({});

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
    const result = await tool.run({ message: 'hi' });
    expect(result).toBe('echo: hi');
  });

  it('returns a friendly string for invalid arguments', async () => {
    const tool = new EchoTool();
    const result = await tool.run({ message: 123 }); // wrong type
    expect(result).toContain('Invalid arguments');
    expect(result).toContain('echo');
  });

  it('catches thrown errors and surfaces them as a string', async () => {
    const tool = new FailingTool();
    const result = await tool.run({});
    expect(result).toContain('Error executing boom');
    expect(result).toContain('kaboom');
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
