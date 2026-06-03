export interface ZeroRedactionOptions {
  replacement?: string;
  extraSensitiveKeys?: readonly string[];
  extraSecretValues?: readonly string[];
  maxDepth?: number;
}

export interface ZeroRedactedError {
  name: string;
  message: string;
  stack?: string;
  cause?: unknown;
  [key: string]: unknown;
}

export interface ZeroSecretRedactor {
  redact(value: unknown): unknown;
  redactString(value: string): string;
  redactError(error: unknown): ZeroRedactedError;
  isSensitiveKey(key: string): boolean;
}
