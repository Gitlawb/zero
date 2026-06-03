import type { Provider } from '../providers/types';
import type { ZeroReasoningEffort, ZeroTokenUsage } from '../zero-model-registry';
import type { ToolCall, ToolResult, ToolSafety } from '../tools/types';
import { toolRegistry } from '../tools';
import { DEFAULT_SYSTEM_PROMPT, PLAN_MODE_SYSTEM_PROMPT } from './prompts';
import { clearPlan } from '../tools/plan';
import { z } from 'zod';

export type AgentPermissionMode = 'auto' | 'ask' | 'unsafe';
export type ToolApprovalDecision = 'allow' | 'deny' | 'allow-session';

export interface ToolApprovalRequest {
  toolCall: ToolCall;
  parsedArgs: unknown;
  safety: ToolSafety;
  reason: string;
  grantKey: string;
}

export interface AgentOptions {
  maxTurns?: number;
  onText?: (text: string) => void;
  onToolCall?: (toolCall: ToolCall) => void;
  onToolResult?: (result: ToolResult) => void;
  onUsage?: (usage: ZeroTokenUsage) => void;
  toolsEnabled?: boolean;   // allows temporarily disabling tool calling for debugging
  debug?: boolean;          // when true, logs the exact payload sent to the provider
  planMode?: boolean;       // when true, the agent plans without modifying the codebase
  permissionMode?: AgentPermissionMode; // ask prompts for gated tools; unsafe grants them for this run
  onToolApproval?: (request: ToolApprovalRequest) => ToolApprovalDecision | Promise<ToolApprovalDecision>;
  enabledTools?: readonly string[];
  disabledTools?: readonly string[];
  reasoningEffort?: ZeroReasoningEffort;
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
  const { 
    maxTurns = 12, 
    onText, 
    onToolCall, 
    onToolResult,
    onUsage,
    toolsEnabled = true,
    debug = false,
    planMode = false,
    permissionMode = 'auto',
    onToolApproval,
    enabledTools,
    disabledTools,
    reasoningEffort,
  } = options;

  // Clear any previous plan when starting a new task
  clearPlan();

  const systemPrompt = planMode ? PLAN_MODE_SYSTEM_PROMPT : DEFAULT_SYSTEM_PROMPT;

  const messages: any[] = [
    { role: 'system', content: systemPrompt },
    { role: 'user', content: initialPrompt },
  ];

  const tools = toolRegistry.getAll();
  // Auto mode only advertises tools that can run without an interactive grant.
  // Ask/unsafe modes can advertise prompt-gated tools, but deny tools are never
  // offered to the model.
  const executableTools = filterExecutableTools(tools, {
    permissionMode,
    enabledTools,
    disabledTools,
  });
  const approvalGrants = new Set<string>();
  let finalAnswer = '';

  for (let turn = 0; turn < maxTurns; turn++) {
    const toolDefinitions = (toolsEnabled && executableTools.length > 0)
      ? executableTools.map(t => {
          // Convert Zod schema to proper JSON Schema (critical for many providers).
          // zod v4 ships this natively — no external package needed.
          const jsonSchema = z.toJSONSchema(t.parameters, {
            target: 'draft-7',
          }) as any;

          // Remove $schema if present (some providers dislike it)
          delete jsonSchema.$schema;

          // Make it strict by default (good practice)
          if (jsonSchema.type === 'object' && !('additionalProperties' in jsonSchema)) {
            jsonSchema.additionalProperties = false;
          }

          return {
            name: t.name,
            description: t.description,
            parameters: jsonSchema,
          };
        })
      : [];

    let currentText = '';
    const toolCallMap = new Map<string, PendingToolCall>();

    if (debug) {
      const red = '\x1b[31m';
      const reset = '\x1b[0m';
      const border = '─'.repeat(50);

      console.log(`\n${red}┌${border}┐`);
      console.log(`│  SENDING TO PROVIDER${' '.repeat(31)}│`);
      console.log(`├${border}┤`);
      console.log(`│ Messages: ${messages.length}${' '.repeat(40 - String(messages.length).length)}│`);
      console.log(`│ Tools enabled: ${toolDefinitions.length > 0}${' '.repeat(33)}│`);
      console.log(`│ Tool count: ${toolDefinitions.length}${' '.repeat(38 - String(toolDefinitions.length).length)}│`);
      if (reasoningEffort) {
        console.log(`│ Reasoning effort: ${reasoningEffort}${' '.repeat(Math.max(0, 31 - reasoningEffort.length))}│`);
      }
      
      if (toolDefinitions.length > 0) {
        const toolsList = toolDefinitions.map(t => t.name).join(', ');
        console.log(`│ Tools: ${toolsList.slice(0, 42)}${' '.repeat(Math.max(0, 43 - toolsList.length))}│`);
        
        // Show a sample of the schema for the first tool (very useful for debugging)
        const firstTool = toolDefinitions[0];
        if (firstTool?.parameters) {
          const schemaPreview = JSON.stringify(firstTool.parameters, null, 2).slice(0, 300);
          console.log(`│ First tool schema sample:\n${schemaPreview}...`);
        }
      }
      
      const preview = String(messages[messages.length-1]?.content || '').slice(0, 45);
      console.log(`│ Last message: ${preview}${' '.repeat(Math.max(0, 36 - preview.length))}│`);
      console.log(`└${border}┘${reset}\n`);
    }

    // Stream the response
    for await (const event of provider.streamCompletion(messages, toolDefinitions)) {
      if (event.type === 'text') {
        currentText += event.content;
        if (onText) onText(event.content);
      }

      if (event.type === 'usage') {
        if (onUsage) {
          onUsage({
            promptTokens: event.promptTokens,
            completionTokens: event.completionTokens,
          });
        }
      }

      if (event.type === 'tool-call-start') {
        const existing = toolCallMap.get(event.id);
        if (existing) {
          existing.name = event.name;
        } else {
          toolCallMap.set(event.id, {
            id: event.id,
            name: event.name,
            arguments: '',
          });
        }
        // Do NOT emit to UI yet — we want the full arguments for proper formatting
      }

      if (event.type === 'tool-call-delta') {
        const existing = toolCallMap.get(event.id) ?? {
          id: event.id,
          name: '',
          arguments: '',
        };
        existing.arguments += event.argumentsFragment;
        toolCallMap.set(event.id, existing);
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

    // Tool execution is serialized so prompt-gated approvals cannot overlap
    // while interactive surfaces keep a single visible pending approval.
    const toolResults: ToolResult[] = [];
    for (const tc of assistantToolCalls) {
      toolResults.push(await executeToolCall(tc, {
        approvalGrants,
        onToolApproval,
        onToolResult,
        permissionMode,
      }));
    }

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

async function executeToolCall(
  tc: ToolCall,
  options: {
    approvalGrants: Set<string>;
    onToolApproval?: AgentOptions['onToolApproval'];
    onToolResult?: AgentOptions['onToolResult'];
    permissionMode: AgentPermissionMode;
  }
): Promise<ToolResult> {
  const emitResult = (result: string): ToolResult => {
    const toolResult = { toolCallId: tc.id, result };
    options.onToolResult?.(toolResult);
    return toolResult;
  };

  let parsedArgs: any = {};
  try {
    parsedArgs = tc.arguments ? JSON.parse(tc.arguments) : {};
  } catch (e: any) {
    return emitResult(`Error: Failed to parse arguments for ${tc.name}: ${e.message}`);
  }

  try {
    const tool = toolRegistry.get(tc.name);
    const grantKey = tool ? `${tool.safety.permission}:${tool.safety.sideEffect}` : `unknown:${tc.name}`;
    let permissionGranted = options.permissionMode === 'unsafe' || tool?.safety.permission === 'allow';

    if (options.permissionMode === 'ask' && tool?.safety.permission === 'prompt') {
      if (options.approvalGrants.has(grantKey)) {
        permissionGranted = true;
      } else if (options.onToolApproval) {
        const decision = await options.onToolApproval({
          toolCall: tc,
          parsedArgs,
          safety: tool.safety,
          reason: tool.safety.reason,
          grantKey,
        });

        if (decision === 'allow' || decision === 'allow-session') {
          permissionGranted = true;
          if (decision === 'allow-session') {
            options.approvalGrants.add(grantKey);
          }
        } else {
          return emitResult(`Permission denied for ${tc.name}: ${tool.safety.reason}`);
        }
      }
    }

    return emitResult(await toolRegistry.run(tc.name, parsedArgs, {
      permissionGranted,
    }));
  } catch (e: any) {
    return emitResult(`Error executing ${tc.name}: ${e.message}`);
  }
}

function filterExecutableTools(
  tools: ReturnType<typeof toolRegistry.getAll>,
  options: {
    permissionMode: AgentPermissionMode;
    enabledTools?: readonly string[];
    disabledTools?: readonly string[];
  }
) {
  const enabled = options.enabledTools ? new Set(options.enabledTools) : undefined;
  const disabled = new Set(options.disabledTools ?? []);

  return tools.filter((tool) => {
    if (enabled && !enabled.has(tool.name)) return false;
    if (disabled.has(tool.name)) return false;

    return options.permissionMode === 'unsafe' || options.permissionMode === 'ask'
      ? tool.safety.permission !== 'deny'
      : tool.safety.permission === 'allow';
  });
}
