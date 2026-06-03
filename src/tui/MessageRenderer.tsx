import React, { useEffect, useState } from 'react';
import { Box, Text } from 'ink';
import { highlightCode } from './highlighter';
import { theme } from './theme';

// Formatter for model responses: paragraphs, inline code, and highlighted code blocks
interface MessageRendererProps {
  content: string;
}

export const MessageRenderer: React.FC<MessageRendererProps> = ({ content }) => {
  const [highlightedBlocks, setHighlightedBlocks] = useState<Map<number, string>>(new Map());

  const codeBlockRegex = /```(\w+)?\n?([\s\S]*?)```/g;
  const parts: React.ReactNode[] = [];
  let lastIndex = 0;
  let match;
  let blockIndex = 0;

  const matches: Array<{ index: number; lang: string; code: string; fullMatch: string }> = [];

  while ((match = codeBlockRegex.exec(content)) !== null) {
    matches.push({
      index: match.index,
      lang: match[1] || 'text',
      code: (match[2] ?? '').trim(),
      fullMatch: match[0],
    });
  }

  // Highlight code blocks asynchronously
  useEffect(() => {
    const highlightAll = async () => {
      const newHighlights = new Map<number, string>();

      for (let i = 0; i < matches.length; i++) {
        const m = matches[i]!;
        try {
          const ansi = await highlightCode(m.code, m.lang);
          newHighlights.set(i, ansi);
        } catch {
          newHighlights.set(i, m.code);
        }
      }

      setHighlightedBlocks(newHighlights);
    };

    if (matches.length > 0) {
      highlightAll();
    }
  }, [content]);

  for (let i = 0; i < matches.length; i++) {
    const m = matches[i]!;

    // Add text before this code block (with paragraph formatting)
    if (m.index > lastIndex) {
      const textBefore = content.slice(lastIndex, m.index);
      parts.push(renderTextBlock(textBefore, `text-${parts.length}`));
    }

    const highlighted = highlightedBlocks.get(i);

    parts.push(
      <Box
        key={`code-${i}`}
        flexDirection="column"
        borderStyle="single"
        borderColor={theme.border.default}
        marginTop={1}
        marginBottom={1}
        paddingX={1}
      >
        {m?.lang && m.lang !== 'text' && (
        <Text color={theme.ui.active} dimColor>{m.lang}</Text>
        )}
        {highlighted ? (
          <Text color={theme.text.secondary}>{highlighted}</Text>
        ) : (
          <Text color={theme.ui.comment}>…</Text>
        )}
      </Box>
    );

    lastIndex = (m?.index ?? 0) + (m?.fullMatch?.length ?? 0);
    blockIndex++;
  }

  // Add remaining text after last code block (with paragraph formatting)
  if (lastIndex < content.length) {
    const remaining = content.slice(lastIndex);
    parts.push(renderTextBlock(remaining, `text-${parts.length}`));
  }

  return <>{parts.length > 0 ? parts : renderTextBlock(content, 'full')}</>;
};

// Stronger formatter for model responses (paragraphs + basic lists + inline formatting)
function renderTextBlock(text: string, keyPrefix: string): React.ReactNode {
  const lines = text.split('\n');

  // Group into logical blocks: paragraphs vs lists
  const blocks: Array<{ type: 'paragraph' | 'list'; lines: string[] }> = [];
  let current: { type: 'paragraph' | 'list'; lines: string[] } | null = null;

  for (const line of lines) {
    const trimmed = line.trim();
    const isListItem = /^\s*(\d+\.|[-*+])\s+/.test(line);

    if (isListItem) {
      if (!current || current.type !== 'list') {
        if (current) blocks.push(current);
        current = { type: 'list', lines: [] };
      }
      current.lines.push(line);
    } else {
      if (!current || current.type !== 'paragraph') {
        if (current) blocks.push(current);
        current = { type: 'paragraph', lines: [] };
      }
      current.lines.push(line);
    }
  }
  if (current) blocks.push(current);

  return (
    <Box key={keyPrefix} flexDirection="column">
      {blocks.map((block, bIndex) => {
        const isLastBlock = bIndex === blocks.length - 1;

        if (block.type === 'list') {
          return (
            <Box
              key={`${keyPrefix}-list-${bIndex}`}
              flexDirection="column"
              marginBottom={isLastBlock ? 0 : 1}
            >
              {block.lines.map((line, lIndex) => (
                <Text key={`${keyPrefix}-l-${lIndex}`}>
                  {formatInline(line)}
                </Text>
              ))}
            </Box>
          );
        }

        // Paragraph block: join lines and split on double newlines for sub-paragraphs
        const paragraphText = block.lines.join('\n').trim();
        const subParagraphs = paragraphText
          .split(/\n\s*\n/)
          .map(p => p.trim())
          .filter(Boolean);

        return (
          <Box
            key={`${keyPrefix}-p-${bIndex}`}
            flexDirection="column"
            marginBottom={isLastBlock ? 0 : 1}
          >
            {subParagraphs.map((p, pIndex) => (
              <Box key={`${keyPrefix}-sp-${pIndex}`} marginBottom={pIndex < subParagraphs.length - 1 ? 1 : 0}>
                <Text>{formatInline(p)}</Text>
              </Box>
            ))}
          </Box>
        );
      })}
    </Box>
  );
}

// Handles inline formatting: **bold**, `inline code`
function formatInline(text: string): React.ReactNode {
  const elements: React.ReactNode[] = [];
  let remaining = text;
  let idx = 0;

  // Simple sequential parser for **bold** and `code`
  while (remaining.length > 0) {
    const boldMatch = remaining.match(/^\*\*([^*]+)\*\*/);
    if (boldMatch) {
      elements.push(
        <Text key={`bold-${idx++}`} bold color={theme.text.primary}>
          {boldMatch[1]}
        </Text>
      );
      remaining = remaining.slice(boldMatch[0].length);
      continue;
    }

    const codeMatch = remaining.match(/^`([^`]+)`/);
    if (codeMatch) {
      elements.push(
        <Text key={`code-${idx++}`} color={theme.ui.comment}>
          {codeMatch[1]}
        </Text>
      );
      remaining = remaining.slice(codeMatch[0].length);
      continue;
    }

    // Take plain text until next special char
    const nextSpecial = remaining.search(/(\*\*|`)/);
    const plain = nextSpecial === -1 ? remaining : remaining.slice(0, nextSpecial);
    if (plain) {
      elements.push(<Text key={`plain-${idx++}`}>{plain}</Text>);
      remaining = remaining.slice(plain.length);
    } else {
      // Fallback single char
      elements.push(<Text key={`plain-${idx++}`}>{remaining[0]}</Text>);
      remaining = remaining.slice(1);
    }
  }

  return <>{elements}</>;
}
