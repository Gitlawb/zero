#!/usr/bin/env node

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const SUMMARY_LIMIT = 280;
const STRUCTURED_FORMATS = new Set(['json', 'stream-json']);

function normalizeSummary(value) {
  return String(value ?? '')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, SUMMARY_LIMIT);
}

function truncateSummary(value) {
  return String(value ?? '').slice(0, SUMMARY_LIMIT);
}

function lastNonEmptyLine(contents) {
  let last = '';
  for (const line of String(contents ?? '').split(/\r?\n/)) {
    if (line.trim()) {
      last = line;
    }
  }
  return last;
}

function summarizeStructured(contents) {
  let summary;

  for (const line of String(contents ?? '').split(/\r?\n/)) {
    if (!line.trim()) {
      continue;
    }

    let event;
    try {
      event = JSON.parse(line);
    } catch {
      continue;
    }

    if (event?.type === 'final' && typeof event.text === 'string') {
      summary = event.text;
    } else if (event?.type === 'error' && typeof event.message === 'string') {
      summary = event.message;
    }
  }

  if (summary !== undefined) {
    return normalizeSummary(summary);
  }
  return normalizeSummary(lastNonEmptyLine(contents));
}

export function summarizeOutput(format, contents) {
  const normalizedFormat = String(format ?? 'text').toLowerCase();
  if (!STRUCTURED_FORMATS.has(normalizedFormat)) {
    return truncateSummary(lastNonEmptyLine(contents));
  }
  return summarizeStructured(contents);
}

function isMainModule() {
  return (
    process.argv[1] !== undefined &&
    pathToFileURL(resolve(process.argv[1])).href === import.meta.url
  );
}

if (isMainModule()) {
  const [format, outputFile] = process.argv.slice(2);
  let contents = '';
  try {
    contents = readFileSync(outputFile, 'utf8');
  } catch {
    contents = '';
  }
  process.stdout.write(`${summarizeOutput(format, contents)}\n`);
}
