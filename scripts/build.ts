import { join } from 'node:path';
import { getGoArch, getGoOS, getZeroArtifactNameForGoOS } from './artifact';

export interface BuildCliOptions {
  goos: string;
  goarch: string;
  output?: string;
  help: boolean;
}

export interface BuildOptions {
  goos: string;
  goarch: string;
  output: string;
  version: string;
}

export function parseBuildArgs(
  args: string[],
  env: NodeJS.ProcessEnv = process.env
): BuildCliOptions {
  const options: BuildCliOptions = {
    goos: env.ZERO_BUILD_GOOS?.trim() || getGoOS(),
    goarch: env.ZERO_BUILD_GOARCH?.trim() || getGoArch(),
    help: false,
  };

  for (let index = 0; index < args.length; index++) {
    const rawArg = args[index]!;
    const { flag, inlineValue } = splitFlagValue(rawArg);

    switch (flag) {
      case '--goos':
        options.goos = readOptionValue(args, inlineValue, ++index, flag);
        if (inlineValue !== undefined) index--;
        break;
      case '--goarch':
        options.goarch = readOptionValue(args, inlineValue, ++index, flag);
        if (inlineValue !== undefined) index--;
        break;
      case '--output':
      case '-o':
        options.output = readOptionValue(args, inlineValue, ++index, flag);
        if (inlineValue !== undefined) index--;
        break;
      case '--help':
      case '-h':
        rejectInlineValue(flag, inlineValue);
        options.help = true;
        break;
      default:
        throw new Error(`Unknown option: ${rawArg}`);
    }
  }

  return options;
}

export function buildHelp(): string {
  return [
    'Usage: bun run scripts/build.ts [options]',
    '',
    'Builds the Go-native zero binary.',
    '',
    'Options:',
    '  --goos <goos>       Target GOOS (default: current platform)',
    '  --goarch <goarch>   Target GOARCH (default: current architecture)',
    '  -o, --output <path> Write binary to path',
    '  -h, --help          Show this help',
    '',
    'Environment overrides:',
    '  ZERO_BUILD_GOOS, ZERO_BUILD_GOARCH',
  ].join('\n');
}

export function defaultBuildOutput(goos: string, cwd = process.cwd()): string {
  return join(cwd, getZeroArtifactNameForGoOS(goos));
}

export function goBuildLdflags(version: string): string {
  return `-s -w -X github.com/Gitlawb/zero/internal/cli.version=${version}`;
}

export function parsePackageVersion(packageText: string): string {
  const parsed = JSON.parse(packageText) as { version?: unknown };

  if (typeof parsed.version !== 'string' || parsed.version.trim() === '') {
    throw new Error('package.json must contain a non-empty string version');
  }

  return parsed.version;
}

export async function buildZeroBinary(options: BuildOptions): Promise<void> {
  const child = Bun.spawn(
    [
      'go',
      'build',
      '-trimpath',
      '-ldflags',
      goBuildLdflags(options.version),
      '-o',
      options.output,
      './cmd/zero',
    ],
    {
      env: {
        ...process.env,
        CGO_ENABLED: process.env.CGO_ENABLED ?? '0',
        GOOS: options.goos,
        GOARCH: options.goarch,
      },
      stderr: 'pipe',
      stdout: 'pipe',
    }
  );

  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);

  if (stdout.trim()) console.log(stdout.trim());

  if (exitCode !== 0) {
    throw new Error(stderr.trim() || `go build exited with ${exitCode}`);
  }
}

async function main(): Promise<void> {
  try {
    const cliOptions = parseBuildArgs(process.argv.slice(2));

    if (cliOptions.help) {
      console.log(buildHelp());
      return;
    }

    const packageText = await Bun.file('package.json').text();
    const version = parsePackageVersion(packageText);
    const output = cliOptions.output ?? defaultBuildOutput(cliOptions.goos);

    await buildZeroBinary({
      goos: cliOptions.goos,
      goarch: cliOptions.goarch,
      output,
      version,
    });

    console.log(`Built ${output} (${cliOptions.goos}/${cliOptions.goarch}, version ${version})`);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.error(`[zero] Build failed: ${message}`);
    process.exitCode = 1;
  }
}

function splitFlagValue(arg: string): { flag: string; inlineValue?: string } {
  const separatorIndex = arg.indexOf('=');
  if (separatorIndex === -1) return { flag: arg };

  return {
    flag: arg.slice(0, separatorIndex),
    inlineValue: arg.slice(separatorIndex + 1),
  };
}

function readOptionValue(
  args: string[],
  inlineValue: string | undefined,
  index: number,
  flag: string
): string {
  if (inlineValue !== undefined) {
    if (inlineValue === '') {
      throw new Error(`${flag} requires a value`);
    }
    return inlineValue;
  }

  return requireValue(args, index, flag);
}

function requireValue(args: string[], index: number, flag: string): string {
  const value = args[index];
  if (!value || value.startsWith('-')) {
    throw new Error(`${flag} requires a value`);
  }
  return value;
}

function rejectInlineValue(flag: string, inlineValue: string | undefined): void {
  if (inlineValue !== undefined) {
    throw new Error(`${flag} does not accept a value`);
  }
}

if (import.meta.main) {
  await main();
}
