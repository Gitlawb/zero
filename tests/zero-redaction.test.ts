import { describe, expect, it } from 'bun:test';
import {
  ZERO_REDACTED_SECRET,
  isZeroSensitiveKey,
  redactZeroError,
  redactZeroSecrets,
  redactZeroString,
} from '../src/zero-redaction';

const OPENAI_KEY = ['sk-proj', 'abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH'].join('-');
const ANTHROPIC_KEY = ['sk-ant-api03', 'abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH'].join('-');
const GITHUB_TOKEN = ['github', 'pat', '11AAAAAAA0abcdefghijklmnopqrstuvwxyz'].join('_');
const JWT =
  'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ6ZXJvIn0.signature123';

describe('zero secret redaction', () => {
  it('redacts sensitive object keys without mutating safe fields', () => {
    const input = {
      model: 'gpt-4.1',
      tokenBudget: 8192,
      authorization: `Bearer ${OPENAI_KEY}`,
      headers: {
        'x-api-key': ANTHROPIC_KEY,
        cookie: 'zero_session=secret-cookie',
        accept: 'application/json',
      },
      nested: {
        environment_secret: 'env-secret-value',
        session_ingress_token: 'session-token-value',
        access_token: GITHUB_TOKEN,
        prompt: 'token budget should remain readable',
      },
    };

    const redacted = redactZeroSecrets(input) as typeof input;

    expect(redacted).not.toBe(input);
    expect(redacted.model).toBe('gpt-4.1');
    expect(redacted.tokenBudget).toBe(8192);
    expect(redacted.headers.accept).toBe('application/json');
    expect(redacted.authorization).toBe(ZERO_REDACTED_SECRET);
    expect(redacted.headers['x-api-key']).toBe(ZERO_REDACTED_SECRET);
    expect(redacted.headers.cookie).toBe(ZERO_REDACTED_SECRET);
    expect(redacted.nested.environment_secret).toBe(ZERO_REDACTED_SECRET);
    expect(redacted.nested.session_ingress_token).toBe(ZERO_REDACTED_SECRET);
    expect(redacted.nested.access_token).toBe(ZERO_REDACTED_SECRET);
    expect(redacted.nested.prompt).toBe('token budget should remain readable');
    expect(input.nested.access_token).toBe(GITHUB_TOKEN);
  });

  it('redacts provider keys, auth headers, URL credentials, query secrets, JWTs, and private keys from text', () => {
    const privateKey = [
      '-----BEGIN PRIVATE KEY-----',
      'abc123',
      '-----END PRIVATE KEY-----',
    ].join('\n');
    const text = [
      `OPENAI_API_KEY=${OPENAI_KEY}`,
      `Authorization: Bearer ${JWT}`,
      `https://zero:${ANTHROPIC_KEY}@example.com/v1?api_key=${OPENAI_KEY}&q=safe`,
      privateKey,
    ].join('\n');

    const redacted = redactZeroString(text);

    expect(redacted).toContain(`OPENAI_API_KEY=${ZERO_REDACTED_SECRET}`);
    expect(redacted).toContain(`Authorization: Bearer ${ZERO_REDACTED_SECRET}`);
    expect(redacted).toContain(
      `https://zero:${ZERO_REDACTED_SECRET}@example.com/v1?api_key=${ZERO_REDACTED_SECRET}&q=safe`
    );
    expect(redacted).not.toContain(OPENAI_KEY);
    expect(redacted).not.toContain(ANTHROPIC_KEY);
    expect(redacted).not.toContain(JWT);
    expect(redacted).not.toContain('abc123');
  });

  it('redacts Error messages and stacks while keeping error metadata', () => {
    const error = new Error(`Provider rejected ${OPENAI_KEY}`);
    error.stack = `Error: Provider rejected ${OPENAI_KEY}\n    at zero (${GITHUB_TOKEN})`;

    const redacted = redactZeroError(error);

    expect(redacted.name).toBe('Error');
    expect(redacted.message).toBe(`Provider rejected ${ZERO_REDACTED_SECRET}`);
    expect(redacted.stack).toContain(ZERO_REDACTED_SECRET);
    expect(redacted.stack).not.toContain(OPENAI_KEY);
    expect(redacted.stack).not.toContain(GITHUB_TOKEN);
  });

  it('handles circular objects and caller-provided exact secret values', () => {
    const input: Record<string, unknown> = {
      name: 'zero',
      safe: 'keep-me',
      provider_response: `custom secret is custom-secret-value`,
    };
    input.self = input;

    const redacted = redactZeroSecrets(input, {
      extraSecretValues: ['custom-secret-value'],
    }) as Record<string, unknown>;

    expect(redacted.name).toBe('zero');
    expect(redacted.safe).toBe('keep-me');
    expect(redacted.provider_response).toBe(`custom secret is ${ZERO_REDACTED_SECRET}`);
    expect(redacted.self).toBe('[Circular]');
  });

  it('classifies sensitive keys without hiding normal token usage fields', () => {
    expect(isZeroSensitiveKey('apiKey')).toBe(true);
    expect(isZeroSensitiveKey('x-api-key')).toBe(true);
    expect(isZeroSensitiveKey('refresh_token')).toBe(true);
    expect(isZeroSensitiveKey('private_key')).toBe(true);
    expect(isZeroSensitiveKey('session_ingress_token')).toBe(true);
    expect(isZeroSensitiveKey('tokenBudget')).toBe(false);
    expect(isZeroSensitiveKey('input_tokens')).toBe(false);
    expect(isZeroSensitiveKey('model')).toBe(false);
  });
});
