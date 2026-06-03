import React from 'react';
import { Box, Text } from 'ink';
import { Logo } from './Logo';
import { MessageRenderer } from './MessageRenderer';
import { ThinkingSpinner } from './Spinner';
import { ToolCallRenderer } from './ToolCallRenderer';
import { listTuiCommands } from './commands';
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
  const startIndex = showLogo ? 0 : scrollOffset;

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
          key={startIndex + index}
          message={msg}
          index={startIndex + index}
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
    const messageWidth = Math.max(1, contentWidth + 3);
    return (
      <Box marginTop={1} width="100%" flexDirection="column">
        <Text color={tuiTheme.colors.userBg}>{'▄'.repeat(messageWidth)}</Text>
        <Box paddingX={1} backgroundColor={tuiTheme.colors.userBg} flexDirection="row" width="100%">
          <Text color={tuiTheme.colors.userSymbol} backgroundColor={tuiTheme.colors.userBg}>{'> '}</Text>
          <Text color={tuiTheme.colors.userSymbol} backgroundColor={tuiTheme.colors.userBg} wrap="wrap">
            {message.content}
          </Text>
        </Box>
        <Text color={tuiTheme.colors.userBg}>{'▀'.repeat(messageWidth)}</Text>
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
      {renderSystemMessage(message.content)}
    </MarkedRow>
  );
}

function renderSystemMessage(content: string): React.ReactNode {
  const commands = new Set(
    listTuiCommands().flatMap((command) => [command.name, ...(command.aliases ?? [])])
  );
  const lines = content.split('\n');
  const lower = content.toLowerCase();
  const toneColor = lower.includes('error') || lower.includes('failed') || lower.includes('authentication')
    ? tuiTheme.colors.danger
    : lower.includes('no provider') || lower.includes('unknown') || lower.includes('not configured')
      ? tuiTheme.colors.warning
      : lower.includes('set to') || lower.includes('enabled') || lower.includes('disabled') || lower.includes('switched') || lower.includes('added')
        ? tuiTheme.colors.success
        : tuiTheme.colors.text;

  return (
    <Box flexDirection="column">
      {lines.map((line, index) => (
        <Text key={`${index}-${line}`} color={index === 0 ? toneColor : tuiTheme.colors.text}>
          {highlightCommands(line, commands)}
        </Text>
      ))}
    </Box>
  );
}

function highlightCommands(text: string, commands: Set<string>): React.ReactNode {
  const parts: React.ReactNode[] = [];
  const regex = /\/[a-zA-Z][\w-]*/g;
  let lastIndex = 0;
  let match;

  while ((match = regex.exec(text)) !== null) {
    const value = match[0];
    if (!commands.has(value)) continue;
    if (match.index > lastIndex) parts.push(text.slice(lastIndex, match.index));
    parts.push(<Text key={`${value}-${match.index}`} color={tuiTheme.colors.accent}>{value}</Text>);
    lastIndex = match.index + value.length;
  }

  if (lastIndex < text.length) parts.push(text.slice(lastIndex));
  return parts.length > 0 ? <>{parts}</> : text;
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
