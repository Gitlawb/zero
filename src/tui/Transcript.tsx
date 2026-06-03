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
}) => {
  return (
    <Box flexGrow={1} flexDirection="row" overflow="hidden">
      <Box flexGrow={1} flexDirection="column" paddingX={1} paddingTop={1}>
        {showLogo && <Logo />}

        {(canScrollUp || canScrollDown) && (
          <Text color={tuiTheme.colors.muted} dimColor>
            {canScrollUp ? '^ ' : '  '}Scroll: Up/Down, PgUp/PgDn, Home/End {canScrollDown ? 'v' : ''}
          </Text>
        )}

        <Box flexDirection="column">
          {visibleMessages.map((msg, index) => (
            <TranscriptRow
              key={scrollOffset + index}
              message={msg}
              index={scrollOffset + index}
              streamingMessageIndex={streamingMessageIndex}
            />
          ))}

          {isThinking && <ThinkingSpinner />}
        </Box>
      </Box>

      {(canScrollUp || canScrollDown) && (
        <Box width={8} alignItems="flex-start" justifyContent="flex-end" paddingTop={1}>
          <Text color={tuiTheme.colors.muted} dimColor>
            {scrollOffset + 1}/{messages.length}
          </Text>
        </Box>
      )}
    </Box>
  );
};

function TranscriptRow({
  message,
  index,
  streamingMessageIndex,
}: {
  message: ChatMessage;
  index: number;
  streamingMessageIndex: number | null;
}) {
  if (message.type === 'user') {
    return (
      <Box marginBottom={1} flexDirection="row">
        <Text color={tuiTheme.colors.accent} bold>{tuiTheme.marks.user} </Text>
        <Text color={tuiTheme.colors.text}>{message.content}</Text>
      </Box>
    );
  }

  if (message.type === 'assistant') {
    const isStreaming = index === streamingMessageIndex;
    return (
      <Box marginBottom={1} flexDirection="row">
        <Text color={tuiTheme.colors.brand} bold>{tuiTheme.marks.assistant} </Text>
        <Box flexDirection="column" flexGrow={1}>
          <MessageRenderer content={message.content} />
          {isStreaming && <Text color={tuiTheme.colors.brand} dimColor>{tuiTheme.marks.cursor}</Text>}
        </Box>
      </Box>
    );
  }

  if (message.type === 'tool-call') {
    const hasResult = !!message.result;
    return (
      <Box marginBottom={0}>
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
    <Box marginBottom={1} flexDirection="row">
      <Text color={tuiTheme.colors.muted}>{tuiTheme.marks.note} </Text>
      <Text color={tuiTheme.colors.muted} dimColor>{message.content}</Text>
    </Box>
  );
}
