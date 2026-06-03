import { z } from 'zod';

export const ZERO_STREAM_JSON_SCHEMA_VERSION = 1;

const IdSchema = z.string().min(1).max(128).regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/);
const NonEmptyStringSchema = z.string().min(1);
const NonNegativeNumberSchema = z.number().finite().nonnegative();

const OutputBaseSchema = z.object({
  schemaVersion: z.literal(ZERO_STREAM_JSON_SCHEMA_VERSION),
  runId: IdSchema,
});

export const ZeroStreamJsonRunStartSchema = OutputBaseSchema.extend({
  type: z.literal('run_start'),
  sessionId: IdSchema.optional(),
  cwd: NonEmptyStringSchema,
  provider: NonEmptyStringSchema,
  model: NonEmptyStringSchema,
  apiModel: NonEmptyStringSchema.optional(),
}).strict();

export const ZeroStreamJsonTextSchema = OutputBaseSchema.extend({
  type: z.literal('text'),
  delta: z.string(),
}).strict();

export const ZeroStreamJsonToolCallSchema = OutputBaseSchema.extend({
  type: z.literal('tool_call'),
  id: IdSchema,
  name: NonEmptyStringSchema,
  args: z.unknown().optional(),
  sideEffect: z.enum(['read', 'write', 'shell', 'network', 'unknown']).optional(),
}).strict();

export const ZeroStreamJsonToolResultSchema = OutputBaseSchema.extend({
  type: z.literal('tool_result'),
  id: IdSchema,
  status: z.enum(['ok', 'error']),
  output: z.string(),
  truncated: z.boolean(),
}).strict();

export const ZeroStreamJsonUsageSchema = OutputBaseSchema.extend({
  type: z.literal('usage'),
  promptTokens: NonNegativeNumberSchema.optional(),
  completionTokens: NonNegativeNumberSchema.optional(),
  totalTokens: NonNegativeNumberSchema.optional(),
  costUsd: NonNegativeNumberSchema.optional(),
}).strict();

export const ZeroStreamJsonFinalSchema = OutputBaseSchema.extend({
  type: z.literal('final'),
  text: z.string(),
}).strict();

export const ZeroStreamJsonWarningSchema = OutputBaseSchema.extend({
  type: z.literal('warning'),
  message: NonEmptyStringSchema,
}).strict();

export const ZeroStreamJsonErrorSchema = OutputBaseSchema.extend({
  type: z.literal('error'),
  code: NonEmptyStringSchema,
  message: NonEmptyStringSchema,
  recoverable: z.boolean(),
}).strict();

export const ZeroStreamJsonRunEndSchema = OutputBaseSchema.extend({
  type: z.literal('run_end'),
  status: z.enum(['success', 'error']),
  exitCode: z.number().int().nonnegative(),
}).strict();

export const ZeroStreamJsonOutputEventSchema = z.discriminatedUnion('type', [
  ZeroStreamJsonRunStartSchema,
  ZeroStreamJsonTextSchema,
  ZeroStreamJsonToolCallSchema,
  ZeroStreamJsonToolResultSchema,
  ZeroStreamJsonUsageSchema,
  ZeroStreamJsonFinalSchema,
  ZeroStreamJsonWarningSchema,
  ZeroStreamJsonErrorSchema,
  ZeroStreamJsonRunEndSchema,
]);

export const ZeroStreamJsonPromptInputSchema = z.object({
  schemaVersion: z.literal(ZERO_STREAM_JSON_SCHEMA_VERSION),
  type: z.literal('prompt'),
  content: NonEmptyStringSchema,
}).strict();

export const ZeroStreamJsonMessageInputSchema = z.object({
  schemaVersion: z.literal(ZERO_STREAM_JSON_SCHEMA_VERSION),
  type: z.literal('message'),
  role: z.literal('user'),
  content: NonEmptyStringSchema,
}).strict();

export const ZeroStreamJsonInputEventSchema = z.discriminatedUnion('type', [
  ZeroStreamJsonPromptInputSchema,
  ZeroStreamJsonMessageInputSchema,
]);

export type ZeroStreamJsonOutputEvent = z.infer<typeof ZeroStreamJsonOutputEventSchema>;
export type ZeroStreamJsonInputEvent = z.infer<typeof ZeroStreamJsonInputEventSchema>;
export type ZeroStreamJsonToolSideEffect = z.infer<
  typeof ZeroStreamJsonToolCallSchema
>['sideEffect'];
