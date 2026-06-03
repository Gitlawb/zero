import { redactZeroSecrets } from '../zero-redaction';
import {
  ZeroStreamJsonInputEventSchema,
  ZeroStreamJsonOutputEventSchema,
  type ZeroStreamJsonInputEvent,
  type ZeroStreamJsonOutputEvent,
} from './types';

export class ZeroStreamJsonProtocolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ZeroStreamJsonProtocolError';
  }
}

export function createZeroStreamJsonRunId(now: Date = new Date()): string {
  const timestamp = now.toISOString().replace(/[-:.TZ]/g, '').slice(0, 14);
  const random = Math.random().toString(36).slice(2, 8);
  return `run_${timestamp}_${random}`;
}

export function formatZeroStreamJsonEvent(event: ZeroStreamJsonOutputEvent): string {
  const safeEvent = redactZeroSecrets(event);
  return JSON.stringify(ZeroStreamJsonOutputEventSchema.parse(safeEvent));
}

export function parseZeroStreamJsonInput(input: string): ZeroStreamJsonInputEvent[] {
  const events: ZeroStreamJsonInputEvent[] = [];
  const lines = input.split(/\r?\n/);

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]?.trim();
    if (!line) continue;

    let parsed: unknown;
    try {
      parsed = JSON.parse(line);
    } catch {
      throw new ZeroStreamJsonProtocolError(
        `Invalid stream-json input at line ${index + 1}: expected a JSON object.`
      );
    }

    const result = ZeroStreamJsonInputEventSchema.safeParse(parsed);
    if (!result.success) {
      throw new ZeroStreamJsonProtocolError(
        `Invalid stream-json input at line ${index + 1}: ${formatSchemaIssue(result.error)}`
      );
    }

    events.push(result.data);
  }

  return events;
}

export function resolveZeroStreamJsonPrompt(events: ZeroStreamJsonInputEvent[]): string {
  const prompt = events
    .map((event) => event.content.trim())
    .filter(Boolean)
    .join('\n\n');

  if (!prompt) {
    throw new ZeroStreamJsonProtocolError(
      'Stream-json input must include at least one prompt or user message event.'
    );
  }

  return prompt;
}

export function parseZeroStreamJsonPrompt(input: string): string {
  return resolveZeroStreamJsonPrompt(parseZeroStreamJsonInput(input));
}

function formatSchemaIssue(error: { issues: Array<{ path: PropertyKey[]; message: string }> }): string {
  const issue = error.issues[0];
  if (!issue) return 'schema validation failed.';

  const path = issue.path.length > 0 ? `${issue.path.map(String).join('.')}: ` : '';
  return `${path}${issue.message}`;
}
