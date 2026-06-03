export type ChatMessage =
  | { type: 'user'; content: string }
  | { type: 'assistant'; content: string }
  | { type: 'tool-call'; id: string; name: string; args: string; result?: string }
  | { type: 'tool-result'; content: string }
  | { type: 'system'; content: string };

export interface TuiModeState {
  isPlanMode: boolean;
  debugMode: boolean;
  toolsEnabled: boolean;
  isThinking: boolean;
}
