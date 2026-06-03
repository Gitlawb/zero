import { describe, it, expect } from 'bun:test';
import { mkdtemp, readFile } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import { z } from 'zod';
import {
  runAgent,
  type ToolApprovalDecision,
  type ToolApprovalRequest,
} from '../src/agent/loop';
import { DEFAULT_SYSTEM_PROMPT, PLAN_MODE_SYSTEM_PROMPT } from '../src/agent/prompts';
import type { Provider, Message, StreamEvent } from '../src/providers/types';
import { toolRegistry } from '../src/tools';
import { ZeroMcpPermissionStore } from '../src/zero-mcp';

// A mock provider that records the messages it receives and replays a
// scripted sequence of stream events per turn.
class MockProvider implements Provider {
  public received: Message[][] = [];
  public receivedTools: any[][] = [];
  private turns: StreamEvent[][];
  private turn = 0;

  constructor(turns: StreamEvent[][]) {
    this.turns = turns;
  }

  async *streamCompletion(messages: Message[], tools: any[] = []): AsyncIterable<StreamEvent> {
    // Snapshot the messages and tool definitions for this turn so tests can inspect the prompt.
    this.received.push(messages.map((m) => ({ ...m })));
    this.receivedTools.push(tools.map((t) => ({ ...t })));
    const events = this.turns[this.turn] ?? [{ type: 'done' as const }];
    this.turn++;
    for (const ev of events) yield ev;
  }
}

async function waitFor(predicate: () => boolean, timeoutMs = 1000): Promise<void> {
  const startedAt = Date.now();
  while (!predicate()) {
    if (Date.now() - startedAt > timeoutMs) {
      throw new Error('Timed out waiting for condition');
    }
    await delay(5);
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

describe('runAgent system prompt selection', () => {
  it('uses the default system prompt when plan mode is off', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'hi' }]]);
    await runAgent('hello', provider, { planMode: false });

    expect(provider.received[0]?.[0]?.role).toBe('system');
    expect(provider.received[0]?.[0]?.content).toBe(DEFAULT_SYSTEM_PROMPT);
  });

  it('uses the plan-mode system prompt when plan mode is on', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'hi' }]]);
    await runAgent('hello', provider, { planMode: true });

    expect(provider.received[0]?.[0]?.content).toBe(PLAN_MODE_SYSTEM_PROMPT);
    expect(provider.received[0]?.[0]?.content).toContain('PLAN MODE IS ACTIVE');
  });
});

describe('runAgent tool-call flow', () => {
  it('executes a tool call and feeds the result back to the model', async () => {
    const provider = new MockProvider([
      // Turn 1: the model asks to update the plan.
      [
        { type: 'tool-call-start', id: 'call_1', name: 'update_plan' },
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({
            plan: [{ id: '1', content: 'do it', status: 'pending' }],
          }),
        },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      // Turn 2: the model produces a final answer.
      [{ type: 'text', content: 'all done' }],
    ]);

    const toolCalls: string[] = [];
    const toolResults: string[] = [];

    const answer = await runAgent('make a plan', provider, {
      onToolCall: (tc) => toolCalls.push(tc.name),
      onToolResult: (r) => toolResults.push(r.result),
    });

    expect(answer).toBe('all done');
    expect(toolCalls).toEqual(['update_plan']);
    expect(toolResults[0]).toContain('do it');

    // Second turn must include the tool result message fed back in.
    const secondTurn = provider.received[1] ?? [];
    const toolMsg = secondTurn.find((m) => m.role === 'tool');
    expect(toolMsg).toBeDefined();
    expect(toolMsg?.content).toContain('do it');
  });

  it('does not advertise prompt-gated tools in automatic permission mode', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'done' }]]);

    await runAgent('which tools can you use?', provider);

    const names = (provider.receivedTools[0] ?? []).map((tool) => tool.name).sort();
    expect(names).toContain('read_file');
    expect(names).toContain('grep');
    expect(names).toContain('glob');
    expect(names).toContain('update_plan');
    expect(names).not.toContain('bash');
    expect(names).not.toContain('apply_patch');
    expect(names).not.toContain('write_file');
    expect(names).not.toContain('edit_file');
  });

  it('advertises prompt-gated tools in unsafe permission mode', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'done' }]]);

    await runAgent('which tools can you use?', provider, {
      permissionMode: 'unsafe',
    });

    const names = (provider.receivedTools[0] ?? []).map((tool) => tool.name).sort();
    expect(names).toContain('read_file');
    expect(names).toContain('grep');
    expect(names).toContain('glob');
    expect(names).toContain('bash');
    expect(names).toContain('apply_patch');
    expect(names).toContain('write_file');
    expect(names).toContain('edit_file');
  });

  it('advertises prompt-gated tools in ask permission mode', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'done' }]]);

    await runAgent('which tools can you use?', provider, {
      permissionMode: 'ask',
    });

    const names = (provider.receivedTools[0] ?? []).map((tool) => tool.name).sort();
    expect(names).toContain('read_file');
    expect(names).toContain('grep');
    expect(names).toContain('glob');
    expect(names).toContain('bash');
    expect(names).toContain('apply_patch');
    expect(names).toContain('write_file');
    expect(names).toContain('edit_file');
  });

  it('respects enabled and disabled tool filters', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'done' }]]);

    await runAgent('which tools can you use?', provider, {
      permissionMode: 'ask',
      enabledTools: ['read_file', 'bash', 'write_file'],
      disabledTools: ['bash'],
    });

    const names = (provider.receivedTools[0] ?? []).map((tool) => tool.name).sort();
    expect(names).toEqual(['read_file', 'write_file']);
  });

  it('does not mutate custom tool JSON schemas while preparing provider tools', async () => {
    const schema = {
      $schema: 'http://json-schema.org/draft-07/schema#',
      type: 'object',
      properties: {},
      additionalProperties: true,
    };
    toolRegistry.register({
      name: 'custom_schema_probe',
      description: 'custom schema probe',
      parameters: z.object({}),
      safety: {
        sideEffect: 'read',
        permission: 'allow',
        reason: 'Test-only schema probe.',
      },
      toJSONSchema: () => schema,
      async execute() {
        return 'ok';
      },
    });
    const provider = new MockProvider([[{ type: 'text', content: 'schema done' }]]);

    try {
      await runAgent('check schema', provider, {
        enabledTools: ['custom_schema_probe'],
      });

      const toolDefinition = provider.receivedTools[0]?.find((tool) => tool.name === 'custom_schema_probe');
      expect(toolDefinition?.parameters.$schema).toBeUndefined();
      expect(schema.$schema).toBe('http://json-schema.org/draft-07/schema#');
    } finally {
      toolRegistry.unregister('custom_schema_probe');
    }
  });

  it('runs prompt-gated tools through the registry only when unsafe mode grants permission', async () => {
    const command = 'echo zero-agent-unsafe';
    const safeProvider = new MockProvider([
      [
        { type: 'tool-call-start', id: 'call_1', name: 'bash' },
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({ command }),
        },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      [{ type: 'text', content: 'safe done' }],
    ]);
    const unsafeProvider = new MockProvider([
      [
        { type: 'tool-call-start', id: 'call_1', name: 'bash' },
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({ command }),
        },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      [{ type: 'text', content: 'unsafe done' }],
    ]);

    const safeResults: string[] = [];
    const unsafeResults: string[] = [];

    await runAgent('try shell', safeProvider, {
      onToolResult: (result) => safeResults.push(result.result),
    });
    await runAgent('try shell', unsafeProvider, {
      permissionMode: 'unsafe',
      onToolResult: (result) => unsafeResults.push(result.result),
    });

    expect(safeResults[0]).toContain('Permission required for bash');
    expect(unsafeResults[0]).toContain('zero-agent-unsafe');
  });

  it('denies prompt-gated tools in ask mode without running them', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'zero-deny-'));
    const path = join(dir, 'denied.txt');
    const provider = new MockProvider([
      [
        { type: 'tool-call-start', id: 'call_1', name: 'write_file' },
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({ path, content: 'nope' }),
        },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      [{ type: 'text', content: 'denied done' }],
    ]);
    const results: string[] = [];

    await runAgent('write file', provider, {
      permissionMode: 'ask',
      onToolApproval: () => 'deny',
      onToolResult: (result) => results.push(result.result),
    });

    expect(results[0]).toContain('Permission denied for write_file');
    expect(await Bun.file(path).exists()).toBe(false);
  });

  it('runs approved prompt-gated tools through the registry path', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'zero-allow-'));
    const path = join(dir, 'allowed.txt');
    const provider = new MockProvider([
      [
        { type: 'tool-call-start', id: 'call_1', name: 'write_file' },
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({ path, content: 'allowed' }),
        },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      [{ type: 'text', content: 'allowed done' }],
    ]);

    await runAgent('write file', provider, {
      permissionMode: 'ask',
      onToolApproval: () => 'allow',
    });

    expect(await readFile(path, 'utf-8')).toBe('allowed');
  });

  it('skips repeated approval after an allow-session decision for the same safety class', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'zero-session-'));
    const firstPath = join(dir, 'first.txt');
    const secondPath = join(dir, 'second.txt');
    const provider = new MockProvider([
      [
        { type: 'tool-call-start', id: 'call_1', name: 'write_file' },
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({ path: firstPath, content: 'first' }),
        },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      [
        { type: 'tool-call-start', id: 'call_2', name: 'write_file' },
        {
          type: 'tool-call-delta',
          id: 'call_2',
          argumentsFragment: JSON.stringify({ path: secondPath, content: 'second' }),
        },
        { type: 'tool-call-end', id: 'call_2' },
      ],
      [{ type: 'text', content: 'session done' }],
    ]);
    let approvalCount = 0;

    await runAgent('write two files', provider, {
      permissionMode: 'ask',
      onToolApproval: () => {
        approvalCount++;
        return 'allow-session';
      },
    });

    expect(approvalCount).toBe(1);
    expect(await readFile(firstPath, 'utf-8')).toBe('first');
    expect(await readFile(secondPath, 'utf-8')).toBe('second');
  });

  it('runs a matching persistently approved MCP tool without prompting again', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'zero-mcp-agent-grant-'));
    const permissionStore = new ZeroMcpPermissionStore({
      filePath: join(dir, 'mcp-permissions.json'),
      now: () => new Date('2026-06-03T09:30:00.000Z'),
    });
    const toolName = 'mcp__persisted_docs__aaaaaaaaaaaa__lookup__00000000';
    await permissionStore.grantTool({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      toolName: 'lookup',
      maxAutonomy: 'medium',
    });
    toolRegistry.register({
      name: toolName,
      description: 'persisted MCP tool',
      parameters: z.object({}),
      safety: {
        sideEffect: 'network',
        permission: 'prompt',
        reason: 'Calls a persisted MCP tool.',
      },
      zeroMcp: {
        serverName: 'docs',
        serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        toolName: 'lookup',
      },
      async execute() {
        return 'persisted mcp ok';
      },
    });
    const provider = new MockProvider([
      [
        { type: 'tool-call-start', id: 'call_1', name: toolName },
        { type: 'tool-call-delta', id: 'call_1', argumentsFragment: '{}' },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      [{ type: 'text', content: 'grant done' }],
    ]);
    let approvalCount = 0;
    const toolResults: string[] = [];

    try {
      const answer = await runAgent('use persisted mcp', provider, {
        permissionMode: 'ask',
        autonomy: 'medium',
        mcpPermissionStore: permissionStore,
        onToolApproval: () => {
          approvalCount++;
          return 'deny';
        },
        onToolResult: (result) => toolResults.push(result.result),
      });

      expect(answer).toBe('grant done');
      expect(approvalCount).toBe(0);
      expect(toolResults[0]).toBe('persisted mcp ok');
    } finally {
      toolRegistry.unregister(toolName);
    }
  });

  it('serializes multiple prompt-gated approvals from the same assistant turn', async () => {
    const provider = new MockProvider([
      [
        { type: 'tool-call-start', id: 'call_1', name: 'bash' },
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({ command: 'echo zero-first' }),
        },
        { type: 'tool-call-end', id: 'call_1' },
        { type: 'tool-call-start', id: 'call_2', name: 'bash' },
        {
          type: 'tool-call-delta',
          id: 'call_2',
          argumentsFragment: JSON.stringify({ command: 'echo zero-second' }),
        },
        { type: 'tool-call-end', id: 'call_2' },
      ],
      [{ type: 'text', content: 'approval done' }],
    ]);
    const approvals: ToolApprovalRequest[] = [];
    const approvalResolvers: Array<(decision: ToolApprovalDecision) => void> = [];
    const resultIds: string[] = [];

    const run = runAgent('run two shell commands', provider, {
      permissionMode: 'ask',
      onToolApproval: (request) => {
        approvals.push(request);
        return new Promise<ToolApprovalDecision>((resolve) => {
          approvalResolvers.push(resolve);
        });
      },
      onToolResult: (result) => resultIds.push(result.toolCallId),
    });

    await waitFor(() => approvalResolvers.length === 1);
    await delay(25);
    expect(approvals.map((approval) => approval.toolCall.id)).toEqual(['call_1']);

    approvalResolvers[0]?.('allow');
    await waitFor(() => approvalResolvers.length === 2);
    expect(approvals.map((approval) => approval.toolCall.id)).toEqual(['call_1', 'call_2']);

    approvalResolvers[1]?.('allow');
    await expect(run).resolves.toBe('approval done');
    expect(resultIds).toEqual(['call_1', 'call_2']);
  });

  it('keeps tool arguments when a delta arrives before the start event', async () => {
    const provider = new MockProvider([
      [
        {
          type: 'tool-call-delta',
          id: 'call_1',
          argumentsFragment: JSON.stringify({
            plan: [{ id: '1', content: 'ordered safely', status: 'pending' }],
          }),
        },
        { type: 'tool-call-start', id: 'call_1', name: 'update_plan' },
        { type: 'tool-call-end', id: 'call_1' },
      ],
      [{ type: 'text', content: 'all done' }],
    ]);

    const toolResults: string[] = [];
    await runAgent('make a plan', provider, {
      onToolResult: (r) => toolResults.push(r.result),
    });

    expect(toolResults[0]).toContain('ordered safely');
  });

  it('stops and returns text when the model makes no tool calls', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'just an answer' }]]);
    const answer = await runAgent('hi', provider, {});
    expect(answer).toBe('just an answer');
    expect(provider.received).toHaveLength(1);
  });
});

describe('runAgent usage events', () => {
  it('forwards provider usage events to backend callers', async () => {
    const provider = new MockProvider([[
      { type: 'usage', promptTokens: 12, completionTokens: 8 },
      { type: 'text', content: 'usage captured' },
    ]]);
    const usageEvents: Array<{ promptTokens?: number; completionTokens?: number }> = [];

    const answer = await runAgent('track usage', provider, {
      onUsage: (usage) => usageEvents.push(usage),
    });

    expect(answer).toBe('usage captured');
    expect(usageEvents).toEqual([
      { promptTokens: 12, completionTokens: 8 },
    ]);
  });
});
