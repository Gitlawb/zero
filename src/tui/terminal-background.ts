const ANSI_BG_COLORS: Record<number, string> = {
  0: '#000000',
  1: '#800000',
  2: '#008000',
  3: '#808000',
  4: '#000080',
  5: '#800080',
  6: '#008080',
  7: '#c0c0c0',
  8: '#808080',
  9: '#ff0000',
  10: '#00ff00',
  11: '#ffff00',
  12: '#0000ff',
  13: '#ff00ff',
  14: '#00ffff',
  15: '#ffffff',
};

export function detectFromColorFgBg(): string | undefined {
  const raw = process.env.COLORFGBG;
  if (!raw) return undefined;
  const last = raw.split(';').at(-1);
  if (last === undefined || last === '') return undefined;
  const index = Number(last);
  return Number.isInteger(index) ? ANSI_BG_COLORS[index] : undefined;
}

function parseOsc11Background(data: string): string | undefined {
  const match = data.match(/\x1b\]11;rgb:([0-9a-fA-F]{1,4})\/([0-9a-fA-F]{1,4})\/([0-9a-fA-F]{1,4})(?:\x07|\x1b\\)/);
  if (!match) return undefined;

  const toByte = (part: string): number => {
    const max = 16 ** part.length - 1;
    return Math.round((parseInt(part, 16) / max) * 255);
  };

  const r = toByte(match[1] ?? '0');
  const g = toByte(match[2] ?? '0');
  const b = toByte(match[3] ?? '0');
  return `#${((r << 16) | (g << 8) | b).toString(16).padStart(6, '0')}`;
}

export async function detectTerminalBackground(timeoutMs = 1000): Promise<string | undefined> {
  const fromEnv = detectFromColorFgBg();
  if (!process.stdin.isTTY || !process.stdout.isTTY) return fromEnv;
  if (typeof process.stdin.setRawMode !== 'function') return fromEnv;

  return await new Promise((resolve) => {
    let buffer = '';
    let finished = false;
    const wasRaw = process.stdin.isRaw;

    const cleanup = () => {
      process.stdin.off('data', onData);
      if (!wasRaw && process.stdin.isRaw) process.stdin.setRawMode(false);
    };

    const finish = (background?: string) => {
      if (finished) return;
      finished = true;
      clearTimeout(timeout);
      cleanup();
      resolve(background ?? fromEnv);
    };

    const onData = (chunk: Buffer) => {
      buffer += chunk.toString('utf8');
      const background = parseOsc11Background(buffer);
      if (background) finish(background);
    };

    const timeout = setTimeout(() => finish(), timeoutMs);

    try {
      if (!wasRaw) process.stdin.setRawMode(true);
      process.stdin.resume();
      process.stdin.on('data', onData);
      process.stdout.write('\x1b]11;?\x07');
    } catch {
      finish();
    }
  });
}
