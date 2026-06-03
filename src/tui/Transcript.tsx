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
  providerName,
  modelName,
}) => {
  return (
    <Box
      flexDirection="column"
      borderStyle="single"
      borderColor={tuiTheme.colors.border}
      paddingX={1}
    >
      {showLogo ? (
        <LaunchPanel
          messages={messages}
          providerName={providerName}
          modelName={modelName}
        />
      ) : (
        <>
          {(canScrollUp || canScrollDown) && (
            <Box flexDirection="row" justifyContent="space-between">
              <Text color={tuiTheme.colors.muted} dimColor>
                transcript history
              </Text>
              <Text color={tuiTheme.colors.muted} dimColor>
                {canScrollUp ? 'older available' : 'top'} / {canScrollDown ? 'newer available' : 'latest'}
              </Text>
            </Box>
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

            {isThinking && (
              <Box marginTop={1} marginLeft={2}>
                <ThinkingSpinner label="zero is reading context" />
              </Box>
            )}
          </Box>
        </>
      )}
    </Box>
  );
};

function LaunchPanel({
  messages,
  providerName,
  modelName,
}: {
  messages: ChatMessage[];
  providerName: string;
  modelName: string;
}) {
  const systemMessages = messages.filter((message) => message.type === 'system');

  return (
    <Box flexDirection="column" paddingY={1}>
      <Logo />
      <Text color={tuiTheme.colors.text} bold>
        Your coding workspace is ready.
      </Text>
      <Text color={tuiTheme.colors.muted} dimColor>
        Start with a file, a bug, a refactor, or a question. Zero keeps the session scoped to this repo.
      </Text>

      <Box borderStyle="single" borderColor={tuiTheme.colors.strongBorder} paddingX={1} marginTop={1} flexDirection="column">
        <Text color={tuiTheme.colors.brand} bold>SESSION</Text>
        <Text>
          <Text color={tuiTheme.colors.muted}>provider </Text>
          <Text color={tuiTheme.colors.text}>{providerName}</Text>
        </Text>
        <Text>
          <Text color={tuiTheme.colors.muted}>model    </Text>
          <Text color={tuiTheme.colors.model}>{modelName}</Text>
        </Text>
        <Text>
          <Text color={tuiTheme.colors.muted}>scope    </Text>
          <Text color={tuiTheme.colors.text}>{process.cwd()}</Text>
        </Text>
      </Box>

      <Box borderStyle="single" borderColor={tuiTheme.colors.border} paddingX={1} marginTop={1} flexDirection="column">
        <Text color={tuiTheme.colors.accent} bold>START FAST</Text>
        <CommandHint command="/provider" hint="configure or switch a provider" />
        <CommandHint command="/model" hint="pick a model for this session" />
        <CommandHint command="/plan" hint="toggle planning behavior" />
        <CommandHint command="/help" hint="show the full command list" />
      </Box>

      {systemMessages.length > 0 && (
        <Box borderStyle="single" borderColor={tuiTheme.colors.border} paddingX={1} flexDirection="column">
          <Text color={tuiTheme.colors.muted} bold>NOTICES</Text>
          {systemMessages.map((message, index) => (
            <Text key={index} color={tuiTheme.colors.muted} dimColor>
              - {message.content}
            </Text>
          ))}
        </Box>
      )}
    </Box>
  );
}

function CommandHint({
  command,
  hint,
}: {
  command: string;
  hint: string;
}) {
  return (
    <Text>
      <Text color={tuiTheme.colors.brand}>{command.padEnd(10)}</Text>
      <Text color={tuiTheme.colors.muted}>{hint}</Text>
    </Text>
  );
}

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
      <Box marginBottom={1} flexDirection="column">
        <Text color={tuiTheme.colors.accent} bold>{tuiTheme.marks.user}</Text>
        <Box borderStyle="single" borderColor={tuiTheme.colors.accent} paddingX={1}>
          <Text color={tuiTheme.colors.text}>{message.content}</Text>
        </Box>
      </Box>
    );
  }

  if (message.type === 'assistant') {
    const isStreaming = index === streamingMessageIndex;
    return (
      <Box marginBottom={1} flexDirection="column">
        <Text color={tuiTheme.colors.brand} bold>{tuiTheme.marks.assistant}</Text>
        <Box borderStyle="single" borderColor={tuiTheme.colors.border} paddingX={1} flexDirection="column">
          <MessageRenderer content={message.content} />
          {isStreaming && <Text backgroundColor={tuiTheme.colors.brand} color={tuiTheme.colors.brand}> </Text>}
        </Box>
      </Box>
    );
  }

  if (message.type === 'tool-call') {
    const hasResult = !!message.result;
    return (
      <Box marginBottom={1} marginLeft={2}>
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
      <Text color={tuiTheme.colors.subtle} bold>{tuiTheme.marks.note} </Text>
      <Text color={tuiTheme.colors.muted} dimColor>{message.content}</Text>
    </Box>
  );
}
