import React from 'react';
import { Box, Text } from 'ink';
import { CommandSuggestions } from './CommandSuggestions';
import { DebugErrorPanel } from './DebugErrorPanel';
import { ToolApprovalPanel } from './ToolApprovalPanel';
import { Transcript } from './Transcript';
import { TuiPromptBox } from './TuiPromptBox';
import { TuiStatusBar } from './TuiStatusBar';
import { tuiTheme } from './theme';
import type { ChatMessage, TuiModeState } from './types';
import type { ToolApprovalRequest } from '../agent/loop';

interface TuiShellProps extends TuiModeState {
  messages: ChatMessage[];
  visibleMessages: ChatMessage[];
  scrollOffset: number;
  streamingMessageIndex: number | null;
  showLogo: boolean;
  canScrollUp: boolean;
  canScrollDown: boolean;
  input: string;
  suggestions: string[];
  suggestionIndex?: number;
  providerName: string;
  modelName: string;
  lastError: any;
  activeFile?: string;
  branch?: string;
  ahead?: number;
  behind?: number;
  totalTokens?: number;
  costUsd?: number;
  contextPercent?: number;
  pendingApproval?: ToolApprovalRequest | null;
  terminalWidth: number;
  terminalColumns?: number;
  terminalHeight: number;
  inputStyle?: 'border' | 'solid';
  inputBackground?: string;
  messageBackground?: string;
}

export const TuiShell: React.FC<TuiShellProps> = ({
  messages,
  visibleMessages,
  scrollOffset,
  streamingMessageIndex,
  showLogo,
  canScrollUp,
  canScrollDown,
  input,
  suggestions,
  suggestionIndex = 0,
  providerName,
  modelName,
  lastError,
  totalTokens,
  costUsd,
  contextPercent,
  pendingApproval,
  terminalWidth,
  terminalColumns,
  inputStyle = 'border',
  inputBackground,
  messageBackground,
  isPlanMode,
  debugMode,
  toolsEnabled,
  isThinking,
}) => {
  const modeState = { isPlanMode, debugMode, toolsEnabled, isThinking };
  const shellWidth = Math.max(60, terminalWidth);
  const rawColumns = Math.max(1, terminalColumns ?? terminalWidth);

  return (
    <Box
      flexDirection="column"
      height="100%"
    >
      <Box flexGrow={1} flexDirection="row" overflow="hidden">
        <Box flexGrow={1} flexDirection="column" paddingX={1} paddingTop={1}>
          <Transcript
            messages={messages}
            visibleMessages={visibleMessages}
            scrollOffset={scrollOffset}
            streamingMessageIndex={streamingMessageIndex}
            isThinking={isThinking}
            showLogo={showLogo}
            canScrollUp={canScrollUp}
            canScrollDown={canScrollDown}
            providerName={providerName}
            modelName={modelName}
            terminalWidth={rawColumns}
            messageBackground={messageBackground}
          />
        </Box>
      </Box>

      {canScrollUp && (
        <Box paddingX={1} justifyContent="flex-end">
          <Text color={tuiTheme.colors.subtle}>
            ↑{scrollOffset}{canScrollDown ? ' ↓' : ''}
          </Text>
        </Box>
      )}

      {debugMode && <DebugErrorPanel error={lastError} />}

      {pendingApproval && <ToolApprovalPanel request={pendingApproval} />}

      <TuiPromptBox
        input={input}
        providerName={providerName}
        modelName={modelName}
        inputStyle={inputStyle}
        inputBackground={inputBackground}
        terminalWidth={rawColumns}
        {...modeState}
      />

      <CommandSuggestions
        suggestions={suggestions}
        selectedIndex={suggestionIndex}
        terminalWidth={shellWidth}
      />

      {suggestions.length === 0 && (
        <TuiStatusBar
          scrollOffset={scrollOffset}
          messageCount={messages.length}
          canScrollUp={canScrollUp}
          canScrollDown={canScrollDown}
          modelName={modelName}
          totalTokens={totalTokens}
          costUsd={costUsd}
          contextPercent={contextPercent}
          {...modeState}
        />
      )}
    </Box>
  );
};
