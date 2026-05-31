import { describe, it, expect } from 'bun:test';
import { runAgent } from '../src/agent/loop';
import { DEFAULT_SYSTEM_PROMPT, PLAN_MODE_SYSTEM_PROMPT } from '../src/agent/prompts';
import type { Provider, Message, StreamEvent } from '../src/providers/types';

// A mock provider that records the messages it receives and replays a
// scripted sequence of stream events per turn.
class MockProvider implements Provider {
  public received: Message[][] = [];
  private turns: StreamEvent[][];
  private turn = 0;

  constructor(turns: StreamEvent[][]) {
    this.turns = turns;
  }

  async *streamCompletion(messages: Message[]): AsyncIterable<StreamEvent> {
    // Snapshot the messages for this turn so tests can inspect the prompt.
    this.received.push(messages.map((m) => ({ ...m })));
    const events = this.turns[this.turn] ?? [{ type: 'done' as const }];
    this.turn++;
    for (const ev of events) yield ev;
  }
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

  it('stops and returns text when the model makes no tool calls', async () => {
    const provider = new MockProvider([[{ type: 'text', content: 'just an answer' }]]);
    const answer = await runAgent('hi', provider, {});
    expect(answer).toBe('just an answer');
    expect(provider.received).toHaveLength(1);
  });
});
