import { describe, it, expect, beforeEach } from 'bun:test';
import { planTool, getCurrentPlan, clearPlan } from '../src/tools/plan';

const samplePlan = [
  { id: '1', content: 'First step', status: 'completed' as const },
  { id: '2', content: 'Second step', status: 'in_progress' as const, notes: 'halfway' },
  { id: '3', content: 'Third step', status: 'pending' as const },
];

describe('planTool', () => {
  beforeEach(() => clearPlan());

  it('stores the plan and reflects it via getCurrentPlan', async () => {
    await planTool.execute({ plan: samplePlan });
    expect(getCurrentPlan()).toEqual(samplePlan);
  });

  it('formats the plan with status markers and notes', async () => {
    const output = await planTool.execute({ plan: samplePlan });
    expect(output).toContain('1. ✓ [completed] First step');
    expect(output).toContain('2. ◉ [in_progress] Second step');
    expect(output).toContain('Notes: halfway');
    expect(output).toContain('3. ○ [pending] Third step');
  });

  it('clearPlan empties the stored plan', async () => {
    await planTool.execute({ plan: samplePlan });
    clearPlan();
    expect(getCurrentPlan()).toEqual([]);
  });

  it('getCurrentPlan returns a copy, not the internal reference', async () => {
    await planTool.execute({ plan: samplePlan });
    const snapshot = getCurrentPlan();
    snapshot.push({ id: 'x', content: 'mutation', status: 'pending' });
    expect(getCurrentPlan()).toHaveLength(samplePlan.length);
  });

  it('rejects invalid status values', async () => {
    await expect(
      planTool.execute({ plan: [{ id: '1', content: 'bad', status: 'nope' }] })
    ).rejects.toThrow();
  });
});
