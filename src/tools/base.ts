import type { z } from 'zod';

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

  /**
   * Run the tool with the (already parsed) arguments.
   * Implementations may throw — the registry will convert thrown errors
   * into a friendly string the model can see.
   */
  abstract execute(args: z.infer<T>): Promise<string>;

  /**
   * Parse raw LLM-supplied arguments through the Zod schema, then execute.
   * Returns either a string result or a `ZodError` formatted as a string.
   */
  async run(rawArgs: unknown): Promise<string> {
    const parsed = this.parameters.safeParse(rawArgs);
    if (!parsed.success) {
      return `Error: Invalid arguments for ${this.name}: ${parsed.error.message}`;
    }
    try {
      return await this.execute(parsed.data as z.infer<T>);
    } catch (err: any) {
      return `Error executing ${this.name}: ${err?.message ?? String(err)}`;
    }
  }

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
