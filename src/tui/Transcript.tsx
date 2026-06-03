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
  providerName,
  modelName,
  terminalWidth,
}) => {
  const dividerWidth = Math.max(32, Math.min(terminalWidth - 12, 84));

  return (
    <Box flexDirection="column" marginTop={1}>
      {showLogo ? (
        <LaunchPanel
          messages={messages}
          providerName={providerName}
          modelName={modelName}
          dividerWidth={dividerWidth}
        />
      ) : (
        <>
          {(canScrollUp || canScrollDown) && (
            <Box marginLeft={6} marginBottom={1} flexDirection="row" justifyContent="space-between">
              <Text color={tuiTheme.colors.muted} dimColor>
                history {scrollOffset + 1}/{messages.length}
              </Text>
              <Text color={tuiTheme.colors.muted} dimColor>
                PgUp/PgDn scroll
              </Text>
            </Box>
          )}

          {visibleMessages.map((msg, index) => (
            <TranscriptRow
              key={scrollOffset + index}
              message={msg}
              index={scrollOffset + index}
              streamingMessageIndex={streamingMessageIndex}
              dividerWidth={dividerWidth}
            />
          ))}

          {isThinking && (
            <ActivityRow marker="*" color={tuiTheme.colors.warning}>
              <ThinkingSpinner label="zero is working" />
            </ActivityRow>
          )}
        </>
      )}
    </Box>
  );
};

function LaunchPanel({
  messages,
  providerName,
  modelName,
  dividerWidth,
}: {
  messages: ChatMessage[];
  providerName: string;
  modelName: string;
  dividerWidth: number;
}) {
  const systemMessages = messages.filter((message) => message.type === 'system');

  return (
    <Box flexDirection="column">
      <ActivityRow marker="*" color={tuiTheme.colors.brand}>
        <Logo />
        <Text color={tuiTheme.colors.text} bold>
          Ready for repo work.
        </Text>
        <Text color={tuiTheme.colors.muted} dimColor>
          Ask for an audit, a fix, a test run, or a focused implementation slice.
        </Text>
      </ActivityRow>

      <ActivityRow marker="1" color={tuiTheme.colors.accent}>
        <SectionTitle label="workspace" width={dividerWidth} />
        <KeyValue label="root" value={process.cwd()} />
        <KeyValue label="provider" value={providerName} />
        <KeyValue label="model" value={modelName} color={tuiTheme.colors.model} />
      </ActivityRow>

      <ActivityRow marker="2" color={tuiTheme.colors.accent}>
        <SectionTitle label="quick commands" width={dividerWidth} />
        <CommandHint command="/provider" hint="providers" />
        <CommandHint command="/model" hint="model switcher" />
        <CommandHint command="/plan" hint="planning mode" />
        <CommandHint command="/tools" hint="tool calling" />
      </ActivityRow>

      {systemMessages.length > 0 && (
        <ActivityRow marker="i" color={tuiTheme.colors.muted}>
          <SectionTitle label="notices" width={dividerWidth} />
          {systemMessages.map((message, index) => (
            <Text key={index} color={tuiTheme.colors.muted} dimColor>
              {message.content}
            </Text>
          ))}
        </ActivityRow>
      )}
    </Box>
  );
}

function TranscriptRow({
  message,
  index,
  streamingMessageIndex,
  dividerWidth,
}: {
  message: ChatMessage;
  index: number;
  streamingMessageIndex: number | null;
  dividerWidth: number;
}) {
  if (message.type === 'user') {
    return (
      <ActivityRow marker=">" color={tuiTheme.colors.accent}>
        <RoleHeader role={tuiTheme.marks.user} color={tuiTheme.colors.accent} width={dividerWidth} />
        <Text color={tuiTheme.colors.text} bold>{message.content}</Text>
      </ActivityRow>
    );
  }

  if (message.type === 'assistant') {
    const isStreaming = index === streamingMessageIndex;

    return (
      <ActivityRow marker="z" color={tuiTheme.colors.brand}>
        <RoleHeader role={tuiTheme.marks.assistant} color={tuiTheme.colors.brand} width={dividerWidth} />
        <MessageRenderer content={message.content} />
        {isStreaming && (
          <Text backgroundColor={tuiTheme.colors.brand} color={tuiTheme.colors.brand}>
            {tuiTheme.marks.cursor}
          </Text>
        )}
      </ActivityRow>
    );
  }

  if (message.type === 'tool-call') {
    const hasResult = !!message.result;
    return (
      <ActivityRow marker="$" color={hasResult ? tuiTheme.colors.success : tuiTheme.colors.warning}>
        <ToolCallRenderer
          name={message.name}
          args={message.args}
          result={message.result}
          status={hasResult ? 'success' : 'running'}
        />
      </ActivityRow>
    );
  }

  if (message.type === 'tool-result') {
    return null;
  }

  return (
    <ActivityRow marker="i" color={tuiTheme.colors.subtle}>
      <Text>
        <Text color={tuiTheme.colors.subtle} bold>{tuiTheme.marks.note} </Text>
        <Text color={tuiTheme.colors.muted} dimColor>{message.content}</Text>
      </Text>
    </ActivityRow>
  );
}

function ActivityRow({
  marker,
  color,
  children,
}: {
  marker: string;
  color: string;
  children: React.ReactNode;
}) {
  return (
    <Box flexDirection="row" marginBottom={1}>
      <Box width={4} alignItems="center" flexDirection="column">
        <Text color={color} bold>{marker}</Text>
        <Text color={tuiTheme.colors.subtle}>|</Text>
      </Box>
      <Box flexDirection="column" flexGrow={1}>
        {children}
      </Box>
    </Box>
  );
}

function SectionTitle({
  label,
  width,
}: {
  label: string;
  width: number;
}) {
  const rule = '-'.repeat(Math.max(8, width - label.length - 3));

  return (
    <Text>
      <Text color={tuiTheme.colors.brand} bold>{label.toUpperCase()}</Text>
      <Text color={tuiTheme.colors.subtle}> {rule}</Text>
    </Text>
  );
}

function RoleHeader({
  role,
  color,
  width,
}: {
  role: string;
  color: string;
  width: number;
}) {
  const rule = '-'.repeat(Math.max(8, width - role.length - 3));

  return (
    <Text>
      <Text color={color} bold>{role}</Text>
      <Text color={tuiTheme.colors.subtle}> {rule}</Text>
    </Text>
  );
}

function KeyValue({
  label,
  value,
  color = tuiTheme.colors.text,
}: {
  label: string;
  value: string;
  color?: string;
}) {
  return (
    <Text>
      <Text color={tuiTheme.colors.muted}>{label.padEnd(9)}</Text>
      <Text color={color}>{value}</Text>
    </Text>
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
      <Text color={tuiTheme.colors.brand} bold>{command.padEnd(11)}</Text>
      <Text color={tuiTheme.colors.muted}>{hint}</Text>
    </Text>
  );
}
