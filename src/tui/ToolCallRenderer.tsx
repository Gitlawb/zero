import React, { useEffect, useState } from 'react';
import { Box, Text } from 'ink';
import { highlightCode } from './highlighter';
import { theme } from './theme';

interface ToolCallRendererProps {
  name: string;
  args: string;
  result?: string;
  status?: 'running' | 'success' | 'error';
}

export const ToolCallRenderer: React.FC<ToolCallRendererProps> = ({
  name,
  args,
  result,
  status = 'success',
}) => {
  const [isExpanded, setIsExpanded] = useState(false);
  const [highlightedArgs, setHighlightedArgs] = useState<string | null>(null);
  const [highlightedResult, setHighlightedResult] = useState<string | null>(null);

  const hasResult = !!result;
  const isLongResult = hasResult && (result!.length > 400 || result!.split('\n').length > 12);
  const [showFullResult, setShowFullResult] = useState(false);

  // Generate a nice one-line summary for collapsed state
  const summary = getToolSummary(name, args);

  // Highlight arguments (only when expanded)
  useEffect(() => {
    if (!isExpanded) return;

    const highlight = async () => {
      try {
        let jsonArgs = args;
        try {
          const parsed = JSON.parse(args);
          jsonArgs = JSON.stringify(parsed, null, 2);
        } catch {
          // not valid JSON
        }
        const ansi = await highlightCode(jsonArgs, 'json');
        setHighlightedArgs(ansi);
      } catch {
        setHighlightedArgs(args);
      }
    };
    highlight();
  }, [args, isExpanded]);

  // Highlight result (only when expanded)
  useEffect(() => {
    if (!isExpanded || !result) return;

    const highlight = async () => {
      try {
        const looksLikeCode =
          result.includes('function') ||
          result.includes('const ') ||
          result.includes('import ') ||
          result.includes('=>') ||
          result.includes('class ');

        const ansi = await highlightCode(result, looksLikeCode ? 'typescript' : 'text');
        setHighlightedResult(ansi);
      } catch {
        setHighlightedResult(result);
      }
    };
    highlight();
  }, [result, isExpanded]);

  const borderColor =
    status === 'running' ? theme.status.warning : status === 'error' ? theme.status.error : theme.status.success;

  const statusIcon =
    status === 'running' ? '●' : status === 'error' ? '✕' : '✓';

  const statusColor =
    status === 'running' ? theme.status.warning : status === 'error' ? theme.status.error : theme.status.success;

  if (!isExpanded) {
    const showToggle = args || hasResult;

    return (
      <Box flexDirection="row" paddingX={1} paddingY={0}>
        <Text color={statusColor} bold>
          {statusIcon}
        </Text>
        <Text color={theme.ui.active} bold> {name}</Text>
        <Text color={theme.ui.comment}>  {summary}</Text>

        {showToggle && (
          <Text
            color={theme.ui.active}
            dimColor
            {...({ onPress: () => setIsExpanded(true) } as any)}
          >
            {'  '}[show]
          </Text>
        )}
      </Box>
    );
  }

  return (
    <Box
      flexDirection="column"
      borderStyle="single"
      borderColor={borderColor}
      paddingX={0}
      paddingY={0}
    >
      <Box paddingX={1} flexDirection="row" justifyContent="space-between">
        <Text color={statusColor} bold>
          {statusIcon} {name}
        </Text>
        <Text
          color={theme.ui.active}
          dimColor
          {...({ onPress: () => setIsExpanded(false) } as any)}
        >
          [hide]
        </Text>
      </Box>

      <Box paddingX={1} paddingTop={0} flexDirection="column">
        <Text color={theme.ui.comment} bold>args</Text>
        {highlightedArgs ? (
          <Text color={theme.text.secondary}>{highlightedArgs}</Text>
        ) : (
          <Text color={theme.ui.comment}>…</Text>
        )}
      </Box>

      {hasResult && (
        <Box paddingX={1} paddingTop={0} flexDirection="column">
          <Text color={theme.ui.comment}>↳</Text>
          {highlightedResult || result ? (
            <Text color={theme.text.secondary}>
              {isLongResult && !showFullResult
                ? (highlightedResult || result!).slice(0, 200) + '...'
                : (highlightedResult || result)}
            </Text>
          ) : null}

          {isLongResult && (
            <Text
              color={theme.ui.active}
              dimColor
              {...({ onPress: () => setShowFullResult(!showFullResult) } as any)}
            >
              {showFullResult ? ' [less]' : ' [more]'}
            </Text>
          )}
        </Box>
      )}
    </Box>
  );
};

// Smart one-line summary for collapsed tool calls.
// We want: "tool name + the command it actually ran" (e.g. "read src/App.tsx", "ls -la .")
function getToolSummary(name: string, args: string): string {
  try {
    const parsed = JSON.parse(args);

    if (name === 'bash') {
      const cmd = parsed.command || '';
      return cmd.length > 65 ? cmd.slice(0, 62) + '...' : cmd;
    }

    if (name === 'read_file') {
      const path = parsed.path || '';
      const short = path.length > 60 ? path.slice(0, 57) + '...' : path;
      return `read ${short}`;
    }

    if (name === 'edit_file') {
      const path = parsed.path || '';
      const short = path.length > 60 ? path.slice(0, 57) + '...' : path;
      return `edit ${short}`;
    }

    // Fallback for future tools
    const keys = Object.keys(parsed);
    if (keys.length > 0) {
      const firstKey = keys[0]!;
      const val = String((parsed as any)[firstKey] ?? '');
      const shortVal = val.length > 50 ? val.slice(0, 47) + '...' : val;
      return `${firstKey}: ${shortVal}`;
    }

    return args.length > 65 ? args.slice(0, 62) + '...' : args;
  } catch {
    return args.length > 65 ? args.slice(0, 62) + '...' : args;
  }
}
