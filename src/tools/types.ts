import type { z } from 'zod';

/**
 * Structural type describing any tool usable by the agent loop.
 *
 * This is a duck-typed interface (matches both plain object literals and
 * subclasses of `ToolBase`). The registry accepts anything that satisfies
 * this shape, which keeps the tool authoring style flexible.
 */
export interface Tool<T extends z.ZodObject<any> = z.ZodObject<any>> {
  name: string;
  description: string;
  parameters: T;
  execute: (args: z.infer<T>) => Promise<string>;
  /**
   * Optional safe-execute path used by the agent loop. When present the
   * loop should prefer it over calling `execute` directly so schema
   * validation and thrown-error handling from `ToolBase.run` are honored.
   * Falls back to `execute` for plain object-literal tools.
   */
  run?: (rawArgs: unknown) => Promise<string>;
}

export interface ToolCall {
  id: string;
  name: string;
  arguments: string; // raw JSON string from model
}

export interface ToolResult {
  toolCallId: string;
  result: string;
}
