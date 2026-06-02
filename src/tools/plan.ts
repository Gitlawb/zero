import { z } from 'zod';
import type { Tool } from './types';

const PlanItemSchema = z.object({
  id: z.string().min(1),
  content: z.string().min(1),
  status: z.enum(['pending', 'in_progress', 'completed', 'failed']),
  notes: z.string().optional(),
});

const UpdatePlanParams = z.object({
  plan: z.array(PlanItemSchema),
});

type PlanItem = z.infer<typeof PlanItemSchema>;

// In-memory plan storage for the current session
let currentPlan: PlanItem[] = [];

function formatPlan(plan: PlanItem[]): string {
  if (plan.length === 0) {
    return 'Plan is currently empty.';
  }

  const statusEmoji: Record<PlanItem['status'], string> = {
    pending: '○',
    in_progress: '◉',
    completed: '✓',
    failed: '✕',
  };

  const lines = plan.map((item, index) => {
    const emoji = statusEmoji[item.status];
    const notes = item.notes ? `\n     Notes: ${item.notes}` : '';
    return `${index + 1}. ${emoji} [${item.status}] ${item.content}${notes}`;
  });

  return 'Current Plan:\n' + lines.join('\n');
}

export const planTool: Tool = {
  name: 'update_plan',
  description: `Create or update the plan for the current task.

Use this tool proactively for any non-trivial task. This is your primary way to organize work, track progress, and communicate your plan to the user.

When to use:
- At the start of a complex task, create a high-level plan first.
- Before making significant changes, break the work into clear steps.
- After completing a step, update its status.
- If the scope changes, revise the plan.

The plan should be clear, actionable, and ordered. Keep it up to date as you work.`,
  parameters: UpdatePlanParams,
  safety: {
    sideEffect: 'read',
    permission: 'allow',
    reason: 'Updates in-memory planning state only.',
  },
  async execute(args) {
    const { plan } = UpdatePlanParams.parse(args);

    currentPlan = plan;

    return formatPlan(currentPlan);
  },
};

// Helper to get the current plan (can be used by other parts of the system later)
export function getCurrentPlan(): PlanItem[] {
  return [...currentPlan];
}

// Helper to clear the plan (useful for new conversations)
export function clearPlan() {
  currentPlan = [];
}

// Export the schema type for potential future use in the TUI
export type { PlanItem };
