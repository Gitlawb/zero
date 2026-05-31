import type { Provider } from '../providers/types';
import type { ToolCall, ToolResult } from '../tools/types';
import { toolRegistry } from '../tools';
import { DEFAULT_SYSTEM_PROMPT } from './prompts';
import { clearPlan } from '../tools/plan';
import { getCurrentPlan, type PlanItem } from '../tools/plan';

export interface AgentOptions {
  maxTurns?: number;
  onText?: (text: string) => void;
  onToolCall?: (toolCall: ToolCall) => void;
  onToolResult?: (result: ToolResult) => void;
  onUsage?: (usage: { promptTokens: number; completionTokens: number }) => void;
  onPlanUpdate?: (plan: PlanItem[]) => void;
}

interface PendingToolCall {
  id: string;
  name: string;
  arguments: string;
}

export async function runAgent(
  initialPrompt: string,
  provider: Provider,
  options: AgentOptions = {}
): Promise<string> {
  const { maxTurns = 12, onText, onToolCall, onToolResult, onUsage, onPlanUpdate } = options;

  // Clear any previous plan when starting a new task
  clearPlan();
  onPlanUpdate?.(getCurrentPlan());

  const messages: any[] = [
    { role: 'system', content: DEFAULT_SYSTEM_PROMPT },
    { role: 'user', content: initialPrompt },
  ];

  const tools = toolRegistry.getAll();
  let finalAnswer = '';

  for (let turn = 0; turn < maxTurns; turn++) {
    const toolDefinitions = tools.map(t => ({
      name: t.name,
      description: t.description,
      parameters: t.parameters.shape,
    }));

    let currentText = '';
    const toolCallMap = new Map<string, PendingToolCall>();

    // Stream the response
    for await (const event of provider.streamCompletion(messages, toolDefinitions)) {
      if (event.type === 'text') {
        currentText += event.content;
        if (onText) onText(event.content);
      }

      if (event.type === 'usage') {
        onUsage?.({
          promptTokens: event.promptTokens,
          completionTokens: event.completionTokens,
        });
      }

      if (event.type === 'tool-call-start') {
        toolCallMap.set(event.id, {
          id: event.id,
          name: event.name,
          arguments: '',
        });
        // Do NOT emit to UI yet — we want the full arguments for proper formatting
      }

      if (event.type === 'tool-call-delta') {
        const existing = toolCallMap.get(event.id);
        if (existing) {
          existing.arguments += event.argumentsFragment;
        }
      }

      if (event.type === 'tool-call-end') {
        // Tool call is now complete (we can execute it later)
      }
    }

    // Convert accumulated tool calls
    const assistantToolCalls: ToolCall[] = Array.from(toolCallMap.values()).map(tc => ({
      id: tc.id,
      name: tc.name,
      arguments: tc.arguments,
    }));

    // Emit complete tool calls to the UI (with full arguments) so the formatter can show the actual command
    if (onToolCall) {
      for (const tc of assistantToolCalls) {
        onToolCall(tc);
      }
    }

    // Add assistant message to history
    messages.push({
      role: 'assistant',
      content: currentText || null,
      toolCalls: assistantToolCalls.length > 0 ? assistantToolCalls : undefined,
    });

    if (assistantToolCalls.length === 0) {
      finalAnswer = currentText;
      break;
    }

    // === Execute tools (in parallel) ===
    const toolPromises = assistantToolCalls.map(async (tc) => {
      const tool = tools.find(t => t.name === tc.name);

      let result: string;

      if (!tool) {
        result = `Error: Unknown tool "${tc.name}".`;
      } else {
        let parsedArgs: any = {};
        try {
          parsedArgs = tc.arguments ? JSON.parse(tc.arguments) : {};
        } catch (e: any) {
          result = `Error: Failed to parse arguments for ${tc.name}: ${e.message}`;
          if (onToolResult) onToolResult({ toolCallId: tc.id, result });
          return { toolCallId: tc.id, result };
        }

        try {
          result = await tool.execute(parsedArgs);
        } catch (e: any) {
          result = `Error executing ${tc.name}: ${e.message}`;
        }
      }

      if (onToolResult) {
        onToolResult({ toolCallId: tc.id, result });
      }

      if (tc.name === 'update_plan') {
        onPlanUpdate?.(getCurrentPlan());
      }

      return { toolCallId: tc.id, result };
    });

    const toolResults = await Promise.all(toolPromises);

    // Feed tool results back into the conversation
    for (const tr of toolResults) {
      messages.push({
        role: 'tool',
        content: tr.result,
        toolCallId: tr.toolCallId,
      });
    }
  }

  return finalAnswer || 'Agent reached maximum number of turns without a final answer.';
}
