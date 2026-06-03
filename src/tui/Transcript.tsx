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
  messageBackground?: string;
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
  messageBackground,
}) => {
  const rows = visibleMessages;
  const startIndex = Math.max(0, messages.length - rows.length - scrollOffset);
  const activeToolCallIndex = rows.reduce(
    (lastIndex, row, index) => row.type === 'tool-call' ? index : lastIndex,
    -1
  );

  return (
    <Box flexDirection="column">
      {showLogo && <Logo maxWidth={terminalWidth - 4} />}

      {(canScrollUp || canScrollDown) && (
        <Text color={tuiTheme.colors.subtle}>
          {canScrollUp ? '↑ ' : '  '}Scroll PgUp/PgDn / Home/End {canScrollDown ? '↓' : ''}
        </Text>
      )}

      {rows.map((msg, index) => (
        <TranscriptRow
          key={startIndex + index}
          message={msg}
          index={startIndex + index}
          streamingMessageIndex={streamingMessageIndex}
          terminalWidth={terminalWidth}
          messageBackground={messageBackground}
          isActiveToolCall={index === activeToolCallIndex}
        />
      ))}

      {isThinking && (
        <Box>
          <ThinkingSpinner />
        </Box>
      )}
    </Box>
  );
};

function TranscriptRow({
  message,
  index,
  streamingMessageIndex,
  terminalWidth,
  messageBackground,
  isActiveToolCall,
}: {
  message: ChatMessage;
  index: number;
  streamingMessageIndex: number | null;
  terminalWidth: number;
  messageBackground?: string;
  isActiveToolCall: boolean;
}) {
  if (message.type === 'user') {
    const backgroundColor = messageBackground ?? tuiTheme.colors.userBg;
    const messageWidth = Math.max(1, terminalWidth - 2);
    return (
      <Box width="100%" flexDirection="column" marginBottom={1}>
        <Box width="100%" height={1}>
          <Text color={backgroundColor}>{'▄'.repeat(messageWidth)}</Text>
        </Box>
        <Box paddingX={1} backgroundColor={backgroundColor} flexDirection="row" width="100%">
          <Text color={tuiTheme.colors.userSymbol} backgroundColor={backgroundColor}>{'> '}</Text>
          <Text color={tuiTheme.colors.userSymbol} backgroundColor={backgroundColor} wrap="wrap">
            {message.content}
          </Text>
        </Box>
        <Box width="100%" height={1}>
          <Text color={backgroundColor}>{'▀'.repeat(messageWidth)}</Text>
        </Box>
      </Box>
    );
  }

  if (message.type === 'assistant') {
    const isStreaming = index === streamingMessageIndex;

    return (
      <Box marginBottom={1} flexDirection="row">
        <Text color={tuiTheme.colors.brand} bold>{'⛬ '}</Text>
        <Box flexDirection="column" flexGrow={1}>
          <MessageRenderer content={message.content} />
          {isStreaming && (
            <Text color={tuiTheme.colors.brand} bold>▌</Text>
          )}
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
          isActive={isActiveToolCall}
        />
      </Box>
    );
  }

  if (message.type === 'tool-result') {
    return null;
  }

  return (
    <Box marginBottom={1}>
      {renderSystemMessage(message.content)}
    </Box>
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
