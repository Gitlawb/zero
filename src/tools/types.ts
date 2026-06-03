import type { z } from 'zod';

/**
 * Durable side-effect class for permission policy.
 *
 * In-memory-only updates, such as update_plan, should use the closest
 * non-durable class and explain the distinction in their safety reason.
 */
export type ToolSideEffect = 'read' | 'write' | 'shell' | 'network' | 'out_of_workspace';
export type ToolPermission = 'allow' | 'prompt' | 'deny';

export interface ToolSafety {
  sideEffect: ToolSideEffect;
  permission: ToolPermission;
  reason: string;
}

/**
 * Structural type describing any tool usable by the agent loop.
 *
 * Current tools use object literals. The registry owns raw argument
 * validation/error handling before calling `execute`.
 */
export interface Tool<T extends z.ZodObject<any> = z.ZodObject<any>> {
  name: string;
  description: string;
  parameters: T;
  safety: ToolSafety;
  zeroMcp?: {
    serverName: string;
    serverIdentity: string;
    toolName: string;
  };
  toJSONSchema?: () => Record<string, unknown>;
  execute: (args: z.infer<T>) => Promise<string>;
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
