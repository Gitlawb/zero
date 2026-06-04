import { existsSync } from 'node:fs';
import { join } from 'node:path';

type Exists = (path: string) => boolean;

export interface NpmWrapperTarget {
  kind: 'native' | 'typescript';
  path: string;
  command: string[];
}

export interface ResolveNpmWrapperTargetOptions {
  root?: string;
  platform?: NodeJS.Platform;
  bunPath?: string;
  args?: string[];
  exists?: Exists;
}

export interface RunNpmWrapperOptions extends ResolveNpmWrapperTargetOptions {
  spawn?: typeof Bun.spawn;
  stderr?: { write(chunk: string): unknown };
}

export function zeroBinaryName(platform: NodeJS.Platform = process.platform): string {
  return platform === 'win32' ? 'zero.exe' : 'zero';
}

export function resolveNpmWrapperTarget(
  options: ResolveNpmWrapperTargetOptions = {}
): NpmWrapperTarget | null {
  const root = options.root ?? process.cwd();
  const args = options.args ?? process.argv.slice(2);
  const exists = options.exists ?? existsSync;
  const platform = options.platform ?? process.platform;
  const bunPath = options.bunPath ?? process.execPath;
  const nativePath = join(root, zeroBinaryName(platform));

  if (exists(nativePath)) {
    return {
      kind: 'native',
      path: nativePath,
      command: [nativePath, ...args],
    };
  }

  const tsPath = join(root, 'src', 'index.ts');
  if (exists(tsPath)) {
    return {
      kind: 'typescript',
      path: tsPath,
      command: [bunPath, tsPath, ...args],
    };
  }

  return null;
}

export async function runNpmWrapper(options: RunNpmWrapperOptions = {}): Promise<number> {
  const target = resolveNpmWrapperTarget(options);
  const stderr = options.stderr ?? process.stderr;
  if (target == null) {
    stderr.write('[zero] No runnable wrapper target found (native binary or src/index.ts). Run `bun run build` before using the npm wrapper.\n');
    return 1;
  }

  try {
    const spawn = options.spawn ?? Bun.spawn;
    const child = spawn(target.command, {
      stdin: 'inherit',
      stdout: 'inherit',
      stderr: 'inherit',
    });

    return await child.exited;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    stderr.write(`[zero] Failed to launch wrapper target: ${message}\n`);
    return 1;
  }
}
