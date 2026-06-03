import React from 'react';
import { Box } from 'ink';
import { CommandSuggestions } from './CommandSuggestions';
import { DebugErrorPanel } from './DebugErrorPanel';
import { ToolApprovalPanel } from './ToolApprovalPanel';
import { StartupScreen } from './StartupScreen';
import { Transcript } from './Transcript';
import { TuiHeader } from './TuiHeader';
import { TuiPromptBox } from './TuiPromptBox';
import { TuiStatusBar } from './TuiStatusBar';
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
  terminalHeight: number;
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
  providerName,
  modelName,
  lastError,
  activeFile,
  branch,
  ahead,
  behind,
  totalTokens,
  costUsd,
  contextPercent,
  pendingApproval,
  terminalWidth,
  terminalHeight,
  isPlanMode,
  debugMode,
  toolsEnabled,
  isThinking,
}) => {
  const modeState = { isPlanMode, debugMode, toolsEnabled, isThinking };
  const shellWidth = Math.max(60, terminalWidth - 1);

  if (showLogo) {
    return (
      <StartupScreen
        providerName={providerName}
        modelName={modelName}
        input={input}
        suggestions={suggestions}
        terminalWidth={terminalWidth}
        terminalHeight={terminalHeight}
      />
    );
  }

  return (
    <Box
      flexDirection="column"
      width={shellWidth}
      paddingX={1}
    >
      <TuiHeader
        providerName={providerName}
        modelName={modelName}
        activeFile={activeFile}
        branch={branch}
        ahead={ahead}
        behind={behind}
        {...modeState}
      />

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
        terminalWidth={shellWidth}
      />

      <CommandSuggestions suggestions={suggestions} />

      {debugMode && <DebugErrorPanel error={lastError} />}

      {pendingApproval && <ToolApprovalPanel request={pendingApproval} />}

      <TuiPromptBox
        input={input}
        providerName={providerName}
        modelName={modelName}
        {...modeState}
      />

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
    </Box>
  );
};
