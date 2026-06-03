import React, { useEffect, useState } from 'react';
import { Box, Text } from 'ink';
import { highlightCode } from './highlighter';
import { tuiTheme } from './theme';

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
  const [showFullResult, setShowFullResult] = useState(false);

  const hasResult = !!result;
  const isLongResult = hasResult && (result!.length > 400 || result!.split('\n').length > 12);
  const summary = getToolSummary(name, args);
  const statusColor = status === 'running'
    ? tuiTheme.colors.warning
    : status === 'error'
      ? tuiTheme.colors.danger
      : tuiTheme.colors.success;
  const statusLabel = status === 'running'
    ? 'run'
    : status === 'error'
      ? 'err'
      : 'ok';

  useEffect(() => {
    if (!isExpanded) return;

    const highlight = async () => {
      try {
        let jsonArgs = args;
        try {
          jsonArgs = JSON.stringify(JSON.parse(args), null, 2);
        } catch {
          // Keep raw arguments when providers send non-JSON text.
        }
        setHighlightedArgs(await highlightCode(jsonArgs, 'json'));
      } catch {
        setHighlightedArgs(args);
      }
    };

    void highlight();
  }, [args, isExpanded]);

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
        setHighlightedResult(await highlightCode(result, looksLikeCode ? 'typescript' : 'text'));
      } catch {
        setHighlightedResult(result);
      }
    };

    void highlight();
  }, [result, isExpanded]);

  if (!isExpanded) {
    return (
      <Box flexDirection="row">
        <Text color={statusColor} bold>{formatToolName(name)}</Text>
        {summary && <Text color={tuiTheme.colors.muted}> {summary}</Text>}
        {(args || hasResult) && (
          <Text
            color={tuiTheme.colors.brand}
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
      borderColor={statusColor}
      paddingX={0}
      paddingY={0}
    >
      <Box paddingX={1} flexDirection="row" justifyContent="space-between">
        <Text color={statusColor} bold>[{statusLabel}] {name}</Text>
        <Text
          color={tuiTheme.colors.brand}
          dimColor
          {...({ onPress: () => setIsExpanded(false) } as any)}
        >
          [hide]
        </Text>
      </Box>

      <Box paddingX={1} flexDirection="column">
        <Text color={tuiTheme.colors.muted} dimColor bold>args</Text>
        <Text>{highlightedArgs || '...'}</Text>
      </Box>

      {hasResult && (
        <Box paddingX={1} flexDirection="column">
          <Text color={tuiTheme.colors.muted} dimColor bold>result</Text>
          <Text color={tuiTheme.colors.success} dimColor>
            {formatResult(highlightedResult || result!, isLongResult, showFullResult)}
          </Text>

          {isLongResult && (
            <Text
              color={tuiTheme.colors.brand}
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

function formatResult(result: string, isLongResult: boolean, showFullResult: boolean): string {
  if (!isLongResult || showFullResult) return result;
  return `${result.slice(0, 200)}...`;
}

function formatToolName(name: string): string {
  if (name === 'bash') return 'Execute';
  if (name === 'read_file') return 'Read';
  if (name === 'write_file') return 'Create';
  if (name === 'edit_file') return 'Edit';
  if (name === 'apply_patch') return 'ApplyPatch';
  if (name === 'grep') return 'Search';
  if (name === 'glob') return 'Glob';
  return name;
}

function getToolSummary(name: string, args: string): string {
  try {
    const parsed = JSON.parse(args);

    if (name === 'bash') {
      return truncate(parsed.command || '', 65);
    }

    if (name === 'read_file') {
      return `read ${truncate(parsed.path || '', 60)}`;
    }

    if (name === 'edit_file') {
      return `edit ${truncate(parsed.path || '', 60)}`;
    }

    if (name === 'write_file') {
      return `write ${truncate(parsed.path || '', 60)}`;
    }

    const firstKey = Object.keys(parsed)[0];
    if (firstKey) {
      return `${firstKey}: ${truncate(String(parsed[firstKey] ?? ''), 50)}`;
    }

    return truncate(args, 65);
  } catch {
    return truncate(args, 65);
  }
}

function truncate(value: string, maxLength: number): string {
  return value.length > maxLength ? `${value.slice(0, maxLength - 3)}...` : value;
}
