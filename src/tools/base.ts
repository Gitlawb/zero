import type { z } from 'zod';
import type { ToolSafety } from './types';

/**
 * Abstract base class for all Zero tools.
 *
 * Subclasses declare:
 *   - a unique `name` and human-readable `description`
 *   - a Zod `parameters` schema describing the inputs the model can call
 *   - an `execute` method that performs the work and returns a string result
 *
 * Tool results are always strings so the LLM can read them directly as
 * `tool` role messages. Errors are caught and surfaced as `Error: ...`
 * strings rather than thrown — this keeps the agent loop resilient and
 * lets the model recover from a bad call on its next turn.
 */
export abstract class ToolBase<T extends z.ZodObject<any> = z.ZodObject<any>> {
  abstract readonly name: string;
  abstract readonly description: string;
  abstract readonly parameters: T;
  abstract readonly safety: ToolSafety;

  /**
   * Run the tool with the (already parsed) arguments.
   * Implementations may throw — the registry will convert thrown errors
   * into a friendly string the model can see.
   */
  abstract execute(args: z.infer<T>): Promise<string>;

  /**
   * JSON Schema (draft-7) representation of the parameters, suitable for
   * sending to OpenAI-compatible providers. We rely on Zod v4's built-in
   * converter so no extra dependency is required.
   */
  toJSONSchema(): Record<string, unknown> {
    const { z } = require('zod') as typeof import('zod');
    const schema = z.toJSONSchema(this.parameters, { target: 'draft-7' }) as Record<string, unknown>;
    delete (schema as { $schema?: string }).$schema;
    if (schema.type === 'object' && !('additionalProperties' in schema)) {
      schema.additionalProperties = false;
    }
    return schema;
  }
}
