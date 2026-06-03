import React from 'react';
import { Box, Text } from 'ink';
import { Logo } from './Logo';
import { MessageRenderer } from './MessageRenderer';
import { ThinkingSpinner } from './Spinner';
import { ToolCallRenderer } from './ToolCallRenderer';
import { tuiTheme } from './theme';
import type { ChatMessage } from './types';

interface TranscriptProps {
  messages: ChatMessage[];
  visibleMessages: ChatMessage[];
  scrollOffset: number;
  streamingMessageIndex: number | null;
  isThinking: boolean;
  showLogo: boolean;
  canScrollUp: boolean;
  canScrollDown: boolean;
  providerName: string;
  modelName: string;
  terminalWidth: number;
}

export const Transcript: React.FC<TranscriptProps> = ({
  messages,
  visibleMessages,
  scrollOffset,
  streamingMessageIndex,
  isThinking,
  showLogo,
  canScrollUp,
  canScrollDown,
  terminalWidth,
}) => {
  const contentWidth = Math.max(40, terminalWidth - 8);
  const rows = showLogo ? messages : visibleMessages;

  return (
    <Box flexDirection="column" marginTop={1}>
      {showLogo && (
        <Box marginBottom={1}>
          <Logo />
        </Box>
      )}

      {(canScrollUp || canScrollDown) && (
        <Box marginLeft={3} marginBottom={1} flexDirection="row" justifyContent="space-between">
          <Text color={tuiTheme.colors.muted} dimColor>
            history {scrollOffset + 1}/{messages.length}
          </Text>
          <Text color={tuiTheme.colors.muted} dimColor>
            PgUp/PgDn scroll
          </Text>
        </Box>
      )}

      {rows.map((msg, index) => (
        <TranscriptRow
          key={showLogo ? index : scrollOffset + index}
          message={msg}
          index={showLogo ? index : scrollOffset + index}
          streamingMessageIndex={streamingMessageIndex}
          contentWidth={contentWidth}
        />
      ))}

      {isThinking && (
        <Box marginTop={1} marginLeft={3}>
          <ThinkingSpinner label="zero is working" />
        </Box>
      )}
    </Box>
  );
};

function TranscriptRow({
  message,
  index,
  streamingMessageIndex,
  contentWidth,
}: {
  message: ChatMessage;
  index: number;
  streamingMessageIndex: number | null;
  contentWidth: number;
}) {
  if (message.type === 'user') {
    return (
      <Box marginTop={1} width="100%" flexDirection="row">
        <Box backgroundColor={tuiTheme.colors.userSymbol} flexShrink={0}>
          <Text color={tuiTheme.colors.panelAlt} backgroundColor={tuiTheme.colors.userSymbol} bold>
            {' > '}
          </Text>
        </Box>
        <Box paddingLeft={2} backgroundColor={tuiTheme.colors.userBg} flexGrow={1}>
          <Text color={tuiTheme.colors.text} backgroundColor={tuiTheme.colors.userBg} wrap="wrap">
            {message.content}{' '}
          </Text>
        </Box>
      </Box>
    );
  }

  if (message.type === 'assistant') {
    const isStreaming = index === streamingMessageIndex;

    return (
      <MarkedRow marker="◆" color={tuiTheme.colors.brand} contentWidth={contentWidth}>
        <MessageRenderer content={message.content} />
        {isStreaming && (
          <Text backgroundColor={tuiTheme.colors.brand} color={tuiTheme.colors.brand}>
            {tuiTheme.marks.cursor}
          </Text>
        )}
      </MarkedRow>
    );
  }

  if (message.type === 'tool-call') {
    const hasResult = !!message.result;
    return (
      <Box marginTop={1} marginLeft={3}>
        <ToolCallRenderer
          name={message.name}
          args={message.args}
          result={message.result}
          status={hasResult ? 'success' : 'running'}
        />
      </Box>
    );
  }

  if (message.type === 'tool-result') {
    return null;
  }

  return (
    <MarkedRow marker="•" color={tuiTheme.colors.muted} contentWidth={contentWidth} compact>
      <Text color={message.content.startsWith('Error:') ? tuiTheme.colors.danger : tuiTheme.colors.muted} dimColor>
        {message.content}
      </Text>
    </MarkedRow>
  );
}

function MarkedRow({
  marker,
  color,
  contentWidth,
  compact = false,
  children,
}: {
  marker: string;
  color: string;
  contentWidth: number;
  compact?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Box marginTop={compact ? 0 : 1} width="100%" flexDirection="row">
      <Box marginRight={1} flexShrink={0}>
        <Text color={color} bold>{marker}</Text>
      </Box>
      <Box width={contentWidth} flexDirection="column">
        {children}
      </Box>
    </Box>
  );
}
