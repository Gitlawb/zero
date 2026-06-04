import { appendFile, mkdir, readFile } from 'fs/promises';
import { dirname, resolve } from 'path';
import { resolveZeroHookPaths } from './config';
import type {
  AppendZeroHookCompletedInput,
  AppendZeroHookStartedInput,
  ZeroHookAuditEvent,
  ZeroHookExecutionCompletedAudit,
  ZeroHookExecutionStartedAudit,
} from './types';

export interface ZeroHookAuditStoreOptions {
  auditPath?: string;
  now?: () => Date;
}

export class ZeroHookAuditStore {
  private readonly auditPath: string;
  private readonly now: () => Date;

  constructor(options: ZeroHookAuditStoreOptions = {}) {
    this.auditPath = resolve(options.auditPath ?? resolveZeroHookPaths().auditPath);
    this.now = options.now ?? (() => new Date());
  }

  async appendStarted(input: AppendZeroHookStartedInput): Promise<ZeroHookExecutionStartedAudit> {
    return await this.append({
      type: 'hook_execution_started',
      ...input,
    });
  }

  async appendCompleted(input: AppendZeroHookCompletedInput): Promise<ZeroHookExecutionCompletedAudit> {
    return await this.append({
      type: 'hook_execution_completed',
      ...input,
    });
  }

  async readEvents(): Promise<ZeroHookAuditEvent[]> {
    let content: string;
    try {
      content = await readFile(this.auditPath, 'utf-8');
    } catch (err: any) {
      if (err?.code === 'ENOENT') return [];
      throw err;
    }

    const events: ZeroHookAuditEvent[] = [];
    const lines = content.split('\n');
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index]?.trim();
      if (!line) continue;

      try {
        events.push(JSON.parse(line) as ZeroHookAuditEvent);
      } catch {
        throw new Error(`Invalid JSON in Zero hook audit at line ${index + 1}`);
      }
    }
    return events;
  }

  private async append<T extends Omit<ZeroHookAuditEvent, 'sequence' | 'createdAt'>>(
    input: T
  ): Promise<T & { sequence: number; createdAt: string }> {
    const sequence = (await this.readEvents()).length + 1;
    const event = {
      sequence,
      createdAt: this.now().toISOString(),
      ...input,
    };

    await mkdir(dirname(this.auditPath), { recursive: true });
    await appendFile(this.auditPath, `${JSON.stringify(event)}\n`, 'utf-8');
    return event;
  }
}
