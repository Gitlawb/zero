import { describe, it, expect } from 'bun:test';
import { z } from 'zod';
import { toolRegistry } from '../src/tools';

// Guards the regression where loop.ts imported a missing `zod-to-json-schema`
// package instead of zod v4's built-in `z.toJSONSchema`.
describe('tool parameter -> JSON Schema conversion', () => {
  it('every registered tool converts to a valid object JSON Schema', () => {
    const tools = toolRegistry.getAll();
    expect(tools.length).toBeGreaterThan(0);

    for (const tool of tools) {
      const schema = z.toJSONSchema(tool.parameters, { target: 'draft-7' }) as any;
      expect(schema.type).toBe('object');
      expect(schema.properties).toBeDefined();
    }
  });

  it('produces the shape the agent loop sends to providers', () => {
    const planTool = toolRegistry.get('update_plan');
    expect(planTool).toBeDefined();

    const schema = z.toJSONSchema(planTool!.parameters, { target: 'draft-7' }) as any;
    expect(schema.properties.plan.type).toBe('array');
    expect(schema.required).toContain('plan');
  });
});
