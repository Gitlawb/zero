import type { ZeroRedactedError, ZeroRedactionOptions, ZeroSecretRedactor } from './types';

export const ZERO_REDACTED_SECRET = '[REDACTED]';
export const ZERO_CIRCULAR_REFERENCE = '[Circular]';

const DEFAULT_MAX_DEPTH = 16;

const DEFAULT_SENSITIVE_KEYS = new Set([
  'access_token',
  'anthropic_api_key',
  'api_key',
  'auth_token',
  'authorization',
  'aws_secret_access_key',
  'aws_session_token',
  'bearer',
  'bearer_token',
  'client_secret',
  'cookie',
  'credential',
  'credentials',
  'environment_secret',
  'gemini_api_key',
  'github_token',
  'gitlab_token',
  'google_api_key',
  'id_token',
  'jwt',
  'npm_token',
  'oauth_token',
  'openai_api_key',
  'passphrase',
  'password',
  'private_key',
  'proxy_authorization',
  'refresh_token',
  'secret',
  'session_ingress_token',
  'session_token',
  'set_cookie',
  'token',
  'x_api_key',
  'zero_api_key',
]);

const SECRET_TEXT_PATTERNS = [
  /\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b/g,
  /\bsk-ant-api\d{2}-[A-Za-z0-9_-]{20,}\b/g,
  /\bgithub_pat_[A-Za-z0-9_]{20,}\b/g,
  /\bgh[pousr]_[A-Za-z0-9_]{20,}\b/g,
  /\bglpat-[A-Za-z0-9_-]{20,}\b/g,
  /\bAIza[0-9A-Za-z_-]{20,}\b/g,
  /\bxox[baprs]-[A-Za-z0-9-]{20,}\b/g,
  /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/g,
  /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/g,
];

const PRIVATE_KEY_PATTERN =
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g;

interface RedactionContext {
  options: ZeroRedactionOptions;
  replacement: string;
  maxDepth: number;
  seen: WeakSet<object>;
}

export function createZeroSecretRedactor(options: ZeroRedactionOptions = {}): ZeroSecretRedactor {
  return {
    redact: (value) => redactZeroSecrets(value, options),
    redactString: (value) => redactZeroString(value, options),
    redactError: (error) => redactZeroError(error, options),
    isSensitiveKey: (key) => isZeroSensitiveKey(key, options),
  };
}

export function isZeroSensitiveKey(key: string, options: ZeroRedactionOptions = {}): boolean {
  const normalized = normalizeZeroSecretKey(key);
  if (!normalized) return false;
  if (DEFAULT_SENSITIVE_KEYS.has(normalized)) return true;

  return (options.extraSensitiveKeys ?? []).some((extraKey) => {
    return normalizeZeroSecretKey(extraKey) === normalized;
  });
}

export function redactZeroString(value: string, options: ZeroRedactionOptions = {}): string {
  const replacement = options.replacement ?? ZERO_REDACTED_SECRET;
  let redacted = value;

  redacted = redactExactSecretValues(redacted, replacement, options.extraSecretValues);
  redacted = redacted.replace(PRIVATE_KEY_PATTERN, replacement);

  redacted = redacted.replace(
    /("([^"\\]*(?:\\.[^"\\]*)*)"\s*:\s*)"([^"\\]*(?:\\.[^"\\]*)*)"/g,
    (match: string, prefix: string, key: string) => {
      return isZeroSensitiveKey(key, options) ? `${prefix}"${replacement}"` : match;
    }
  );

  redacted = redacted.replace(
    /\b([A-Za-z_][A-Za-z0-9_.-]*)(\s*=\s*)(["']?)([^"'\s&]+)\3/g,
    (match: string, key: string, assignment: string, quote: string) => {
      return isZeroSensitiveKey(key, options)
        ? `${key}${assignment}${quote}${replacement}${quote}`
        : match;
    }
  );

  redacted = redacted.replace(
    /\b(authorization|proxy-authorization)\s*:\s*(bearer|basic)\s+([^\s,;]+)/gi,
    (_match: string, header: string, scheme: string) => {
      return `${header}: ${scheme} ${replacement}`;
    }
  );

  redacted = redacted.replace(
    /\b(x-api-key|api-key|cookie|set-cookie)\s*:\s*([^\r\n]+)/gi,
    (_match: string, header: string) => {
      return `${header}: ${replacement}`;
    }
  );

  redacted = redacted.replace(
    /\b((?:https?|wss?|ftp):\/\/[^:\s/@]+:)([^@\s/]+)(@)/gi,
    (_match: string, prefix: string, _password: string, suffix: string) => {
      return `${prefix}${replacement}${suffix}`;
    }
  );

  redacted = redacted.replace(
    /([?&])([^=&#\s]+)=([^&#\s]+)/g,
    (match: string, separator: string, key: string) => {
      return isZeroSensitiveKey(key, options) ? `${separator}${key}=${replacement}` : match;
    }
  );

  for (const pattern of SECRET_TEXT_PATTERNS) {
    redacted = redacted.replace(pattern, replacement);
  }

  return redacted;
}

export function redactZeroSecrets(value: unknown, options: ZeroRedactionOptions = {}): unknown {
  return redactValue(value, createRedactionContext(options), 0);
}

export function redactZeroError(error: unknown, options: ZeroRedactionOptions = {}): ZeroRedactedError {
  const context = createRedactionContext(options);

  if (!(error instanceof Error)) {
    return {
      name: 'Error',
      message: redactZeroString(String(error), options),
    };
  }

  const redacted: ZeroRedactedError = {
    name: error.name || 'Error',
    message: redactZeroString(error.message, options),
  };

  if (error.stack) {
    redacted.stack = redactZeroString(error.stack, options);
  }

  if ('cause' in error && error.cause !== undefined) {
    redacted.cause = redactValue(error.cause, context, 1);
  }

  for (const [key, entryValue] of Object.entries(error)) {
    if (key === 'name' || key === 'message' || key === 'stack' || key === 'cause') {
      continue;
    }
    redacted[key] = isZeroSensitiveKey(key, options)
      ? context.replacement
      : redactValue(entryValue, context, 1);
  }

  return redacted;
}

export function redactZeroErrorMessage(error: unknown, options: ZeroRedactionOptions = {}): string {
  return redactZeroError(error, options).message;
}

function redactValue(value: unknown, context: RedactionContext, depth: number): unknown {
  if (typeof value === 'string') {
    return redactZeroString(value, context.options);
  }

  if (
    value === null ||
    typeof value === 'undefined' ||
    typeof value === 'number' ||
    typeof value === 'boolean' ||
    typeof value === 'bigint' ||
    typeof value === 'symbol' ||
    typeof value === 'function'
  ) {
    return value;
  }

  if (value instanceof Error) {
    return redactZeroError(value, context.options);
  }

  if (value instanceof Date) {
    return new Date(value.getTime());
  }

  if (depth >= context.maxDepth) {
    return '[MaxDepth]';
  }

  if (typeof value !== 'object') {
    return value;
  }

  if (context.seen.has(value)) {
    return ZERO_CIRCULAR_REFERENCE;
  }
  context.seen.add(value);

  if (isHeaders(value)) {
    const headers: Record<string, string> = {};
    for (const [key, headerValue] of value.entries()) {
      headers[key] = isZeroSensitiveKey(key, context.options)
        ? context.replacement
        : redactZeroString(headerValue, context.options);
    }
    return headers;
  }

  if (Array.isArray(value)) {
    return value.map((item) => redactValue(item, context, depth + 1));
  }

  if (value instanceof Map) {
    const redactedMap = new Map<unknown, unknown>();
    for (const [mapKey, mapValue] of value.entries()) {
      const keyText = typeof mapKey === 'string' ? mapKey : String(mapKey);
      redactedMap.set(
        mapKey,
        isZeroSensitiveKey(keyText, context.options)
          ? context.replacement
          : redactValue(mapValue, context, depth + 1)
      );
    }
    return redactedMap;
  }

  if (value instanceof Set) {
    return new Set([...value].map((item) => redactValue(item, context, depth + 1)));
  }

  const redactedObject: Record<string, unknown> = {};
  for (const [key, entryValue] of Object.entries(value)) {
    redactedObject[key] = isZeroSensitiveKey(key, context.options)
      ? context.replacement
      : redactValue(entryValue, context, depth + 1);
  }

  return redactedObject;
}

function createRedactionContext(options: ZeroRedactionOptions): RedactionContext {
  return {
    options,
    replacement: options.replacement ?? ZERO_REDACTED_SECRET,
    maxDepth: options.maxDepth ?? DEFAULT_MAX_DEPTH,
    seen: new WeakSet<object>(),
  };
}

function normalizeZeroSecretKey(key: string): string {
  return key
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
}

function redactExactSecretValues(
  value: string,
  replacement: string,
  exactSecrets: readonly string[] | undefined
): string {
  const secrets = [...(exactSecrets ?? [])]
    .filter((secret) => secret.length > 0)
    .sort((a, b) => b.length - a.length);

  let redacted = value;
  for (const secret of secrets) {
    redacted = redacted.replace(new RegExp(escapeRegExp(secret), 'g'), replacement);
  }
  return redacted;
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function isHeaders(value: object): value is Headers {
  return typeof Headers !== 'undefined' && value instanceof Headers;
}
